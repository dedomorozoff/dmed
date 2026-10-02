package editor

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dmed/internal/agent"
	"dmed/internal/ai"
	"dmed/internal/config"
)

// SUB_AGENT delegates a self-contained piece of work to a nested agent that runs
// in the background while the chat waits.
//
// Two rules shape everything here:
//
//  1. A sub-agent never writes. It produces Changes through the same tool code
//     the chat uses, and they land in the same queue, the same diff review and
//     the same Applier. Delegation cannot become a back door around review.
//  2. A sub-agent runs on the queue worker goroutine, so it may not touch the
//     editor Model. Its tools therefore go through toolEnv — the same
//     implementations, without any editor state.

// subAgentDepth is how deep delegation may nest. One level means a sub-agent
// cannot spawn a sub-sub-agent: the tool is simply absent from its list.
const subAgentDepth = 1

// subAgentTimeout bounds one delegation. The chat is parked while it runs, so
// without a cap a wedged provider would leave the conversation stuck.
const subAgentTimeout = 180 * time.Second

// subAgentDef describes the delegation tool to the chat model.
func subAgentDef() ai.ToolDef {
	return ai.ToolDef{
		Name: "SUB_AGENT",
		Description: "Delegate a self-contained task to a background sub-agent and wait for it. Use it for " +
			"work that needs many steps on its own (a rename across files, writing a test suite, " +
			"researching one module). The sub-agent investigates and proposes changes; a human " +
			"reviews every change before anything is written. It cannot run shell commands, cannot " +
			"ask the user anything, and cannot delegate further, so give it a complete task. " +
			"You get a one-line report when it finishes.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task": map[string]any{
					"type":        "string",
					"description": "the complete task for the sub-agent, including the goal and any constraints",
				},
			},
			"required": []string{"task"},
		},
	}
}

// subAgentTools is the tool surface handed to a sub-agent: read and propose,
// nothing else. RUN is excluded because its confirmation is a park, and a
// background task has nobody to answer it; ASK_USER for the same reason;
// SUB_AGENT so depth stays at one.
type subAgentTools struct {
	env toolEnv
}

func newSubAgentTools(root string, cfg config.Config) *subAgentTools {
	return &subAgentTools{env: newToolEnv(root, configAI{
		SkippedDirs:    cfg.Editor.SkippedDirs,
		RestrictToRoot: cfg.AI.RestrictToRoot,
	})}
}

// subAgentAllowed lists the sub-agent's tools. Kept as a positive list on
// purpose: a new tool must be added here consciously, not inherited.
func subAgentAllowed() map[string]bool {
	return map[string]bool{"READ": true, "SEARCH": true, "LIST_DIR": true, "GLOB": true, "REPLACE": true, "EDIT": true}
}

// Defs implements agent.ToolExecutor.
func (t *subAgentTools) Defs() []ai.ToolDef {
	allowed := subAgentAllowed()
	out := make([]ai.ToolDef, 0, len(allowed))
	for _, d := range coreToolDefs() {
		if allowed[toolKey(d.Name)] {
			out = append(out, d)
		}
	}
	return out
}

// Exec implements agent.ToolExecutor: it runs one tool and returns the text
// plus a proposed change. There is no Park result here by construction — a
// sub-agent has no way to ask the user anything.
func (t *subAgentTools) Exec(name, args string) (string, *agent.Change) {
	switch normalizeToolName(name) {
	case "READ":
		return t.env.read(argOf(args)), nil
	case "SEARCH":
		var a struct {
			Arg   string `json:"arg"`
			Regex bool   `json:"regex"`
		}
		if err := json.Unmarshal([]byte(args), &a); err != nil {
			return "[SEARCH error] " + err.Error(), nil
		}
		return t.env.search(a.Arg, a.Regex), nil
	case "LIST_DIR":
		return t.env.listDir(argOf(args)), nil
	case "GLOB":
		return t.env.glob(argOf(args)), nil
	case "REPLACE":
		var a struct {
			Path    string `json:"path"`
			Search  string `json:"search"`
			Replace string `json:"replace"`
		}
		if err := json.Unmarshal([]byte(args), &a); err != nil {
			return "[REPLACE error] " + err.Error(), nil
		}
		return t.env.replace(a.Path, a.Search, a.Replace)
	case "EDIT":
		var a struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal([]byte(args), &a); err != nil {
			return "[EDIT error] " + err.Error(), nil
		}
		return t.env.edit(a.Path, a.Content)
	default:
		return "[sub_agent] tool " + name + " is not available to a sub-agent", nil
	}
}

// argOf pulls the single "arg" argument the str-shaped tools take.
func argOf(args string) string {
	var a struct {
		Arg string `json:"arg"`
	}
	_ = json.Unmarshal([]byte(args), &a)
	return a.Arg
}

// execSubAgent queues a delegated task and parks the chat until it finishes.
//
// The delegation is asynchronous on purpose: the tool loop runs on the UI
// thread, so waiting inline would freeze the editor for the length of the task.
func (m *Model) execSubAgent(taskText string) ToolResult {
	taskText = strings.TrimSpace(taskText)
	if taskText == "" {
		return ToolResult{Text: "[SUB_AGENT error] empty task"}
	}
	if m.agentQueue == nil {
		return ToolResult{Text: "[SUB_AGENT error] the agent queue is not running yet — try again in a moment"}
	}
	task := m.agentQueue.EnqueueSub(taskText, m.chatThreadLabel())
	return ToolResult{
		Text: fmt.Sprintf("[SUB_AGENT] delegated: %s\n(waiting for the sub-agent; review its changes in the agent panel)",
			taskText),
		Park: Park{Kind: ParkSubAgent, Text: task.ID},
	}
}

// chatThreadLabel identifies where a delegation came from, so the agent panel
// can say why the task exists.
func (m *Model) chatThreadLabel() string {
	if m.chatModel == "" {
		return "chat"
	}
	return "chat:" + m.chatModel
}

// startSubAgent runs one queued sub-agent task on the queue worker goroutine.
// It is called by the worker, which already owns dispatch, and every goroutine
// body runs under debug.CapturePanicReport.
func (m *Model) startSubAgent(task agent.Task) {
	tools := newSubAgentTools(m.baseDir(), m.cfg)
	sub := agent.NewSubagent(m.agentRunner.Provider(), m.agentQueue, tools)
	sub.Options = m.aiRequestOptions()
	sub.Base = m.baseDir()
	sub.Cfg = agent.SubagentConfig{
		Prompt:    strings.TrimSpace(m.cfg.Agent.SubagentPrompt),
		MaxRounds: m.cfg.Agent.SubagentRounds,
		Timeout:   subAgentTimeout,
	}
	_, _ = sub.Run(m.agentCtx, task)
}

// pollSubAgentPark advances a chat parked on a delegation. It is called from the
// update loop, which is the only place allowed to touch the model — the worker
// goroutine just records the task state on the queue.
//
// It reports whether the park was resolved.
func (m *Model) pollSubAgentPark() bool {
	park := m.chatPark
	if park == nil || park.park.Kind != ParkSubAgent || m.agentQueue == nil {
		return false
	}
	task, ok := m.agentQueue.Find(park.park.Text)
	if !ok || !task.Terminal() {
		return false
	}
	m.chatPark = nil
	m.msg = ""
	text := subAgentReport(task)
	resolveParkedResult(m.chatPendingResults, park.resultIdx, text)
	m.rebuildChatRows()
	if m.startNextPark() {
		return true
	}
	m.finalizeChatTools()
	return true
}

// subAgentReport is what the chat model learns when the delegation returns. It
// says what happened and where the diff waits, because the sub-agent's changes
// are reviewed in the agent panel, not inline here.
func subAgentReport(task agent.Task) string {
	switch task.Status {
	case agent.StatusFailed:
		return "[SUB_AGENT failed] " + task.Error + " — the task is in the agent panel with the error."
	case agent.StatusCancelled:
		return "[SUB_AGENT cancelled] the user stopped the sub-agent."
	}
	if len(task.Changes) == 0 {
		return "[SUB_AGENT done] the sub-agent investigated and proposed no changes."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[SUB_AGENT done] proposed %d file change(s):", len(task.Changes))
	for _, c := range task.Changes {
		fmt.Fprintf(&b, "\n  %s", shortenPathForReport(c.Path))
	}
	b.WriteString("\nReview them in the agent panel (Alt+L): nothing is written until you accept the diff.")
	return b.String()
}

// shortenPathForReport prints a path relative to the project when possible, so
// the chat does not fill up with absolute paths.
func shortenPathForReport(p string) string { return shortenPath("", p) }

// cancelParkedSubAgent lets the user stop a delegation with Esc, instead of
// being stuck waiting for a sub-agent that may take minutes.
func (m *Model) cancelParkedSubAgent() {
	park := m.chatPark
	if park == nil || park.park.Kind != ParkSubAgent || m.agentQueue == nil {
		return
	}
	if m.agentRunner != nil {
		m.agentRunner.Cancel(park.park.Text)
	}
	m.agentQueue.Cancel(park.park.Text)
}

// subAgentRunning reports whether a delegation is in flight, used to keep the
// status line honest.
func (m *Model) subAgentRunning() bool {
	if m.agentQueue == nil {
		return false
	}
	for _, t := range m.agentQueue.Snapshot() {
		if t.Kind == agent.KindSubagent && !t.Terminal() {
			return true
		}
	}
	return false
}
