package editor

import (
	"os"
	"path/filepath"
	"strings"
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

// TestAgentAcceptOpensFilesInTabs verifies that accepting a reviewed agent
// task opens the touched files in tabs (focusing already-open ones without
// duplicating) and reports created/modified counts.
func TestAgentAcceptOpensFilesInTabs(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.go"), "package old\n")
	writeTestFile(t, filepath.Join(dir, "b.go"), "package b\n")

	// a.go is already open in a tab; b.go is not.
	m := New()
	m.root = dir
	m.openPath(filepath.Join(dir, "a.go"))
	before := len(m.tabs)

	m.agentQueue = agent.NewQueue(nil)
	m.agentApplier = agent.NewApplier()
	ap := agent.NewApplier()
	ap.Read = func(p string) (string, error) {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		return string(b), err
	}
	ap.Write = func(p, c string) error {
		return os.WriteFile(filepath.Join(dir, filepath.FromSlash(p)), []byte(c), 0o644)
	}
	m.agentApplier = ap
	fg := &agentFakeGit{}
	fb := &agentFakeBus{}
	m.agentCommit = &agent.Committer{Repo: fg, Bus: fb}

	task := m.agentQueue.Enqueue("refactor")
	m.agentQueue.Next()
	m.agentQueue.SetChanges(task.ID, []agent.Change{
		{Path: "a.go", Orig: "package old\n", New: "package new\n"},
		{Path: "b.go", Orig: "package b\n", New: "package b2\n"},
	})

	m.startAgentReview(task.ID)
	m.acceptAgentReview()

	if got := m.agentQueue.Find(task.ID).Status; got != agent.StatusApplied {
		t.Fatalf("status = %s, want applied", got)
	}
	if len(m.tabs) != before+1 {
		t.Fatalf("expected exactly one new tab (b.go), tabs before=%d now=%d", before, len(m.tabs))
	}
	// a.go was already open: its content should now reflect the applied change.
	for _, tb := range m.tabs {
		if filepath.Base(tb.path) == "a.go" && tb.buf.Text() != "package new\n" {
			t.Fatalf("a.go tab content = %q, want updated content", tb.buf.Text())
		}
		if filepath.Base(tb.path) == "b.go" && tb.buf.Text() != "package b2\n" {
			t.Fatalf("b.go tab content = %q, want updated content", tb.buf.Text())
		}
	}
}

// TestAgentPanelFocusToggle verifies Alt+L moves focus between the editor and
// the agent panel without collapsing it, and that Tab/Esc behave sanely.
func TestAgentPanelFocusToggle(t *testing.T) {
	m := New()
	m.agentQueue = agent.NewQueue(nil)

	// Alt+L opens and focuses the panel.
	m = press(m, tea.KeyPressMsg{Code: 'l', Mod: tea.ModAlt})
	if !m.agentOpen || !m.agentFocus {
		t.Fatalf("alt+l must open+focus panel: open=%v focus=%v", m.agentOpen, m.agentFocus)
	}

	// Alt+L again returns focus to the editor while the panel stays open.
	m = press(m, tea.KeyPressMsg{Code: 'l', Mod: tea.ModAlt})
	if !m.agentOpen {
		t.Fatal("toggling focus must keep the panel open")
	}
	if m.agentFocus {
		t.Fatal("alt+l while open must release focus back to the editor")
	}

	// While unfocused, typing reaches the buffer.
	m = press(m, tea.KeyPressMsg{Text: "x"})
	if got := m.cur().buf.Text(); !strings.Contains(got, "x") {
		t.Fatalf("buffer after typing while panel open = %q", got)
	}

	// Alt+L moves focus back into the panel.
	m = press(m, tea.KeyPressMsg{Code: 'l', Mod: tea.ModAlt})
	if !m.agentFocus {
		t.Fatal("alt+l must refocus the panel")
	}

	// Tab returns to the editor without closing the panel.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyTab})
	if !m.agentOpen || m.agentFocus {
		t.Fatalf("tab must keep the panel open but release focus: open=%v focus=%v", m.agentOpen, m.agentFocus)
	}

	// Refocus, then Esc closes the panel entirely.
	m = press(m, tea.KeyPressMsg{Code: 'l', Mod: tea.ModAlt})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.agentOpen || m.agentFocus {
		t.Fatalf("esc must close the panel: open=%v focus=%v", m.agentOpen, m.agentFocus)
	}
}
