package editor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dmed/internal/agent"
	"dmed/internal/ai"
)

// Agent tasks persist to a JSON file next to the project (like the chat
// history and the session), so a review that was not finished before a
// restart — or before a crash — is still there when the panel opens again.
//
// The file holds the same shape the queue holds: a task with its proposed
// changes. A running or queued task cannot survive an editor that is gone
// (nobody streamed it), so it is stored as cancelled with a fixed explanation;
// restoring therefore needs no judgement, the queue just takes what it reads.

const (
	agentStoreFileName = ".dmed_agent.json"
	maxAgentTasks      = 50
	// agentInterrupted is the error line a mid-flight task gets on its way to
	// disk. It names the cause, because "cancelled" without one reads like
	// the user pressed the key.
	agentInterrupted = "interrupted — the editor exited"
)

// persistedAgentTask is the on-disk shape of one task. Field names are
// explicit so the file stays readable (and hand-fixable) across versions.
type persistedAgentTask struct {
	ID      string         `json:"id"`
	Prompt  string         `json:"prompt"`
	Kind    agent.TaskKind `json:"kind,omitempty"`
	Parent  string         `json:"parent,omitempty"`
	Status  agent.Status   `json:"status"`
	Changes []agent.Change `json:"changes,omitempty"`
	Usage   ai.Usage       `json:"usage,omitempty"`
	Error   string         `json:"error,omitempty"`
	Created time.Time      `json:"created"`
	Updated time.Time      `json:"updated"`
}

func (p persistedAgentTask) toTask() agent.Task {
	return agent.Task{
		ID: p.ID, Prompt: p.Prompt, Kind: p.Kind, Parent: p.Parent,
		Status: p.Status, Changes: p.Changes, Usage: p.Usage, Error: p.Error,
		Created: p.Created, Updated: p.Updated,
	}
}

func agentStorePath(root string) string {
	if root != "" {
		return filepath.Join(root, agentStoreFileName)
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, agentStoreFileName)
	}
	return agentStoreFileName
}

// storeView normalises a snapshot for the file: nothing in flight survives
// as in flight.
func storeView(tasks []agent.Task) []persistedAgentTask {
	if len(tasks) > maxAgentTasks {
		tasks = tasks[len(tasks)-maxAgentTasks:]
	}
	out := make([]persistedAgentTask, 0, len(tasks))
	for _, t := range tasks {
		p := persistedAgentTask{
			ID: t.ID, Prompt: t.Prompt, Kind: t.Kind, Parent: t.Parent,
			Status: t.Status, Changes: t.Changes, Usage: t.Usage, Error: t.Error,
			Created: t.Created, Updated: t.Updated,
		}
		if p.Status == agent.StatusRunning || p.Status == agent.StatusQueued {
			p.Status = agent.StatusCancelled
			p.Error = agentInterrupted
		}
		out = append(out, p)
	}
	return out
}

// storeSignature is what the dirty-check compares: IDs, statuses and errors.
// Progress ticks must not rewrite the file — a running task changes progress
// dozens of times per second and nothing else the store holds.
func storeSignature(tasks []persistedAgentTask) string {
	var b strings.Builder
	for _, t := range tasks {
		b.WriteString(t.ID)
		b.WriteByte(':')
		b.WriteString(string(t.Status))
		b.WriteByte(':')
		b.WriteString(t.Error)
		b.WriteByte(';')
	}
	return b.String()
}

// saveAgentStore writes the queue snapshot when its durable shape moved.
// m.agentStoreSig remembers what is on disk, so the common case — a refresh
// repaint that changed only progress — writes nothing at all.
func (m *Model) saveAgentStore() {
	if !m.cfg.Agent.Persist || m.agentQueue == nil {
		return
	}
	view := storeView(m.agentQueue.Snapshot())
	sig := storeSignature(view)
	if sig == m.agentStoreSig {
		return
	}
	if err := writeAgentStore(agentStorePath(m.root), view); err != nil {
		m.agentStoreSig = sig // a failing disk must not restick the save per refresh
		return
	}
	m.agentStoreSig = sig
}

// forceSaveAgentStore writes unconditionally; the quit path uses it so the
// interrupted marks land even if the signature machinery lost track.
func (m *Model) forceSaveAgentStore() {
	if !m.cfg.Agent.Persist || m.agentQueue == nil {
		return
	}
	view := storeView(m.agentQueue.Snapshot())
	_ = writeAgentStore(agentStorePath(m.root), view)
	m.agentStoreSig = storeSignature(view)
}

func writeAgentStore(path string, view []persistedAgentTask) error {
	data, err := json.MarshalIndent(view, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// loadAgentStore restores yesterday's tasks into a fresh queue. A corrupt
// file starts the queue empty rather than failing the editor open.
func (m *Model) loadAgentStore(q *agent.Queue) {
	if !m.cfg.Agent.Persist {
		return
	}
	data, err := os.ReadFile(agentStorePath(m.root))
	if err != nil {
		return
	}
	var view []persistedAgentTask
	if json.Unmarshal(data, &view) != nil {
		return
	}
	tasks := make([]agent.Task, 0, len(view))
	for _, p := range view {
		tasks = append(tasks, p.toTask())
	}
	if n := q.Restore(tasks); n > 0 {
		m.msg = m.t("msg.agent_restored", n)
	}
	// Whatever was written is what the queue now holds: prime the signature
	// so the first refresh does not rewrite the identical file.
	m.agentStoreSig = storeSignature(storeView(q.Snapshot()))
}
