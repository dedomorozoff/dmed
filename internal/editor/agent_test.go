package editor

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/go-git/go-git/v5/plumbing"

	"dmed/internal/agent"
	"dmed/internal/events"
)

// TestAgentPromptQueuesTask verifies the task-prompt flow adds a task to the
// queue without touching the AI provider.
func TestAgentPromptQueuesTask(t *testing.T) {
	m := New()
	m.agentQueue = agent.NewQueue(nil)
	m.agentPrompt = true
	m.agentPromptIn = nil

	// type "fix the parser"
	for _, r := range []rune("fix the parser") {
		m.handleAgentPrompt(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m.handleAgentPrompt(tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.agentPrompt {
		t.Fatalf("prompt should close after submit")
	}
	tasks := m.agentQueue.Snapshot()
	if len(tasks) != 1 {
		t.Fatalf("expected 1 queued task, got %d", len(tasks))
	}
	if tasks[0].Prompt != "fix the parser" {
		t.Fatalf("prompt = %q", tasks[0].Prompt)
	}
	if tasks[0].Status != agent.StatusQueued {
		t.Fatalf("status = %s, want queued", tasks[0].Status)
	}
}

func TestAgentPromptEscDiscards(t *testing.T) {
	m := New()
	m.agentQueue = agent.NewQueue(nil)
	m.agentPrompt = true
	m.handleAgentPrompt(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m.handleAgentPrompt(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.agentPrompt {
		t.Fatalf("prompt should close on esc")
	}
	if len(m.agentQueue.Snapshot()) != 0 {
		t.Fatalf("no task should be queued on esc")
	}
}

func TestAgentStatusLabels(t *testing.T) {
	for s, want := range map[agent.Status]string{
		agent.StatusQueued:    "Q",
		agent.StatusRunning:   "R",
		agent.StatusReview:    "▶",
		agent.StatusApplied:   "A",
		agent.StatusDone:      "✓",
		agent.StatusFailed:    "!",
		agent.StatusCancelled: "×",
	} {
		if got := agentStatusLabel(s); got != want {
			t.Fatalf("label(%s) = %q, want %q", s, got, want)
		}
	}
}

func TestProgressBar(t *testing.T) {
	if got := progressBar(0.5, 4); got != "[██··]" {
		t.Fatalf("progressBar(0.5,4) = %q", got)
	}
	if got := progressBar(1, 4); got != "[████]" {
		t.Fatalf("progressBar(1,4) = %q", got)
	}
}

type agentFakeGit struct {
	staged []string
}

func (g *agentFakeGit) Stage(path string) error              { g.staged = append(g.staged, path); return nil }
func (g *agentFakeGit) Commit(string) (plumbing.Hash, error) { return plumbing.NewHash("x"), nil }

type agentFakeBus struct {
	events []events.Event
}

func (b *agentFakeBus) Publish(e events.Event) { b.events = append(b.events, e) }

func TestAgentReviewAndAccept(t *testing.T) {
	fs := map[string]string{"a.go": "package old\n"}
	m := New()
	m.agentQueue = agent.NewQueue(nil)
	m.agentApplier = agent.NewApplier()
	m.agentApplier.Read = func(p string) (string, error) { return fs[p], nil }
	m.agentApplier.Write = func(p, c string) error { fs[p] = c; return nil }
	fg := &agentFakeGit{}
	fb := &agentFakeBus{}
	m.agentCommit = &agent.Committer{Repo: fg, Bus: fb}

	task := m.agentQueue.Enqueue("refactor")
	m.agentQueue.Next()
	m.agentQueue.SetChanges(task.ID, []agent.Change{
		{Path: "a.go", Orig: "package old\n", New: "package new\n"},
	})

	m.startAgentReview(task.ID)
	if !m.agentReviewMode {
		t.Fatalf("review mode not entered")
	}
	if len(m.agentReviewRows) == 0 {
		t.Fatalf("no diff rows loaded")
	}

	// Accept should apply + commit + mark applied + close review.
	m.acceptAgentReview()
	if m.agentReviewMode {
		t.Fatalf("review mode should close after accept")
	}
	if fs["a.go"] != "package new\n" {
		t.Fatalf("file not applied: %q", fs["a.go"])
	}
	fresh := m.agentQueue.Find(task.ID)
	if fresh.Status != agent.StatusApplied {
		t.Fatalf("status = %s, want applied", fresh.Status)
	}
	if len(fg.staged) != 1 || fg.staged[0] != "a.go" {
		t.Fatalf("staged = %v", fg.staged)
	}
	if len(fb.events) < 2 {
		t.Fatalf("expected refresh events, got %d", len(fb.events))
	}
}

func TestAgentReviewReject(t *testing.T) {
	m := New()
	m.agentQueue = agent.NewQueue(nil)
	m.agentApplier = agent.NewApplier()
	m.agentCommit = &agent.Committer{}

	task := m.agentQueue.Enqueue("task")
	m.agentQueue.Next()
	m.agentQueue.SetChanges(task.ID, []agent.Change{{Path: "a.go", Orig: "x", New: "y"}})

	m.startAgentReview(task.ID)
	m.rejectAgentReview()
	if m.agentReviewMode {
		t.Fatalf("review mode should close after reject")
	}
	if got := m.agentQueue.Find(task.ID).Status; got != agent.StatusDone {
		t.Fatalf("status = %s, want done", got)
	}
}
