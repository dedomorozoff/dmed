package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dmed/internal/ai"
)

// A sub-agent is a nested task the chat delegates with SUB_AGENT: it runs its
// own tool loop in the background and returns the changes it proposed, which
// then go through the very same diff review as a task the user started by hand.
// It never writes anything itself — the executor it is handed produces Changes,
// nothing else.
//
// The loop lives here, in the TUI-free agent package; the actual tools are
// supplied by the caller through ToolExecutor, so a sub-agent reuses exactly the
// implementations the chat uses instead of a second, drifting copy.

// ToolExecutor is the tool surface a sub-agent may use. The editor implements it.
// Defs decides which tools exist — a tool that is not there cannot be called,
// which is how RUN, ASK_USER and SUB_AGENT are withheld from a sub-agent.
type ToolExecutor interface {
	// Defs returns the tool definitions for this sub-agent.
	Defs() []ai.ToolDef
	// Exec runs one tool call and returns the text to feed back to the model
	// plus an optional proposed change. A parked result (a tool waiting for a
	// human) is not representable here on purpose: a background task has nobody
	// to answer it, so the editor simply does not offer such tools.
	Exec(name, args string) (text string, change *Change)
}

// SubagentConfig carries the settings a delegated task needs. The zero value is
// usable: the built-in prompt and a sane round cap apply.
type SubagentConfig struct {
	// Prompt replaces the built-in sub-agent instruction. Empty means the
	// built-in one.
	Prompt string
	// MaxRounds caps the tool loop. Zero means defaultSubagentRounds.
	MaxRounds int
	// Timeout bounds the whole task. Zero means defaultSubagentTimeout.
	Timeout time.Duration
}

const (
	// defaultSubagentRounds bounds the loop. A sub-agent is a helper, not a
	// second main loop: two rounds of reading plus one of editing is plenty.
	defaultSubagentRounds = 6
	// defaultSubagentTimeout is the wall-clock cap in seconds. Without it a
	// wedged provider would leave the chat parked forever, because the chat
	// waits for the delegation to finish.
	defaultSubagentTimeout = 180 * time.Second
)

// Subagent runs one delegated task against a provider.
type Subagent struct {
	prov  ai.Provider
	exec  ToolExecutor
	queue *Queue
	// Options carries generation parameters forwarded to the provider.
	Options ai.Options
	// Base is the project root, used in messages so the model knows where it is.
	Base string
	Cfg  SubagentConfig
}

// NewSubagent wires a sub-agent to a provider, a queue and a tool executor.
func NewSubagent(prov ai.Provider, queue *Queue, exec ToolExecutor) *Subagent {
	return &Subagent{prov: prov, queue: queue, exec: exec}
}

// Result is what a finished sub-agent produced.
type Result struct {
	Summary string
	Changes []Change
}

// Run executes the task and stores whatever it proposed on the queue task, so
// the task ends up in review like any other agent work. It blocks until the
// task finishes, fails or is cancelled; callers run it on the queue worker
// goroutine. The task is taken by value for the same reason Runner.Run takes
// one: the queue owns its storage.
func (s *Subagent) Run(parent context.Context, task Task) (Result, error) {
	if s.prov == nil {
		return Result{}, fmt.Errorf("no AI provider configured")
	}
	if s.exec == nil {
		return Result{}, fmt.Errorf("sub-agent has no tools")
	}

	ctx := parent
	if d := s.timeout(); d > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}

	msgs := s.buildMessages(task)
	var changes []Change
	added := map[string]bool{} // one proposal per path: the last one wins
	rounds := s.maxRounds()

	for round := 0; round < rounds; round++ {
		if ctx.Err() != nil {
			s.finish(task.ID, nil, summary(changes))
			return Result{Changes: changes}, ctx.Err()
		}
		calls, text, err := s.round(ctx, msgs)
		if text != "" {
			s.queue.SetProgress(task.ID, progressByRound(round, rounds))
		}
		if err != nil {
			if ctx.Err() != nil {
				s.finish(task.ID, nil, summary(changes))
				return Result{Changes: changes}, ctx.Err()
			}
			s.queue.SetError(task.ID, err.Error())
			return Result{Changes: changes}, err
		}
		if len(calls) == 0 {
			// The model answered without tools: the task is done.
			s.finish(task.ID, changes, summary(changes))
			return Result{Summary: text, Changes: changes}, nil
		}
		msgs = append(msgs, ai.Message{Role: "assistant", Content: text, ToolCalls: calls})
		for _, c := range calls {
			res, chg := s.exec.Exec(c.Name, c.Args)
			if chg != nil {
				changes = mergeChange(changes, added, *chg)
			}
			msgs = append(msgs, ai.Message{Role: "tool", ToolCallID: c.ID, ToolName: c.Name, Content: res})
		}
	}

	// Out of rounds: keep whatever was proposed and say so, instead of silently
	// dropping work the model already did.
	s.finish(task.ID, changes, summary(changes)+" (round limit reached)")
	return Result{Changes: changes}, fmt.Errorf("sub-agent exceeded %d tool rounds", rounds)
}

// round runs one provider call and returns its tool calls plus the text it
// streamed.
func (s *Subagent) round(ctx context.Context, msgs []ai.Message) ([]ai.ToolCall, string, error) {
	var (
		calls []ai.ToolCall
		text  strings.Builder
	)
	err := s.prov.ChatStream(ctx, ai.Request{
		Messages: msgs,
		Tools:    s.exec.Defs(),
		Options:  s.Options,
	}, ai.Handler{
		Delta:     func(d string) { text.WriteString(d) },
		ToolCalls: func(c []ai.ToolCall) { calls = append(calls, c...) },
	})
	return calls, text.String(), err
}

// finish stores the proposed changes on the task. With none, the task simply
// finishes: a sub-agent that investigated and changed nothing is a valid answer.
func (s *Subagent) finish(id string, changes []Change, sum string) {
	if len(changes) == 0 {
		s.queue.SetStatus(id, StatusDone)
		s.queue.SetProgress(id, 1)
		return
	}
	s.queue.SetChanges(id, changes)
	_ = sum // the summary travels in the chat, the panel shows the diff
}

func (s *Subagent) maxRounds() int {
	if s.Cfg.MaxRounds > 0 {
		return s.Cfg.MaxRounds
	}
	return defaultSubagentRounds
}

func (s *Subagent) timeout() time.Duration {
	if s.Cfg.Timeout > 0 {
		return s.Cfg.Timeout
	}
	return defaultSubagentTimeout
}

func (s *Subagent) buildMessages(task Task) []ai.Message {
	sys := strings.TrimSpace(s.Cfg.Prompt)
	if sys == "" {
		sys = defaultSubagentPrompt
	}
	if s.Base != "" {
		sys += "\n\nProject root: " + s.Base
	}
	return []ai.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: "Task: " + task.Prompt},
	}
}

// defaultSubagentPrompt states the rule that keeps a sub-agent useful and
// bounded: investigate, propose, stop. The writer is not the sub-agent.
const defaultSubagentPrompt = "You are a sub-agent working inside a code editor on one delegated task. " +
	"Investigate with the read-only tools, then propose the change with EDIT or REPLACE. " +
	"Every proposal is reviewed by a human before anything is written, so propose the change " +
	"itself instead of describing it. Do the whole task, then answer in one or two sentences: " +
	"what you changed and what you could not finish."

// mergeChange keeps one proposal per path: a sub-agent that edits a file three
// times must not hand the reviewer three conflicting diffs.
func mergeChange(changes []Change, seen map[string]bool, c Change) []Change {
	if seen[c.Path] {
		for i := range changes {
			if changes[i].Path == c.Path {
				changes[i] = c
				return changes
			}
		}
	}
	seen[c.Path] = true
	return append(changes, c)
}

// summary is the one-line report the chat shows when the delegation returns.
func summary(changes []Change) string {
	if len(changes) == 0 {
		return "sub-agent finished without proposing changes"
	}
	return fmt.Sprintf("sub-agent proposed %d file change(s)", len(changes))
}

// progressByRound maps the loop position onto 0..0.9, so the panel shows motion
// while the sub-agent works.
func progressByRound(round, rounds int) float32 {
	if rounds <= 1 {
		return 0.5
	}
	p := float32(round) / float32(rounds-1)
	if p > 0.9 {
		p = 0.9
	}
	return p
}
