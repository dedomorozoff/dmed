package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"dmed/internal/ai"
)

// scriptedProvider replays a fixed list of turns, so a sub-agent loop can be
// driven without a network or a real model.
type scriptedProvider struct {
	turns []scriptedTurn
	i     int
	// lastReq records the request of the final turn, for assertions.
	lastReq ai.Request
}

type scriptedTurn struct {
	content string
	calls   []ai.ToolCall
	err     error
}

func (p *scriptedProvider) Models(context.Context) ([]string, error) { return []string{"m"}, nil }

func (p *scriptedProvider) ChatStream(_ context.Context, req ai.Request, h ai.Handler) error {
	p.lastReq = req
	if p.i >= len(p.turns) {
		return errors.New("scripted provider exhausted")
	}
	t := p.turns[p.i]
	p.i++
	if t.err != nil {
		return t.err
	}
	if t.content != "" && h.Delta != nil {
		h.Delta(t.content)
	}
	if len(t.calls) > 0 && h.ToolCalls != nil {
		h.ToolCalls(t.calls)
	}
	return nil
}

// fakeExec records the tool calls a sub-agent made and hands back canned results.
type fakeExec struct {
	calls   []string
	results map[string]textChange
	// seq hands out a different result per call of the same tool, for testing
	// how repeated edits to one file are merged.
	seq  map[string][]textChange
	used map[string]int
}

type textChange struct {
	text string
	chg  *Change
}

func (f *fakeExec) Defs() []ai.ToolDef {
	return []ai.ToolDef{{Name: "READ"}, {Name: "EDIT"}}
}

func (f *fakeExec) Exec(name, args string) (string, *Change) {
	f.calls = append(f.calls, name+" "+args)
	if list, ok := f.seq[name]; ok {
		i := f.used[name]
		f.used[name] = i + 1
		if i < len(list) {
			return list[i].text, list[i].chg
		}
		return "[no more scripted results]", nil
	}
	r, ok := f.results[name]
	if !ok {
		return "[" + name + " done]", nil
	}
	return r.text, r.chg
}

func TestSubagentProposesChangesAndGoesToReview(t *testing.T) {
	prov := &scriptedProvider{turns: []scriptedTurn{
		{calls: []ai.ToolCall{{ID: "c1", Name: "READ", Args: `{"arg":"a.go"}`}}},
		{calls: []ai.ToolCall{{ID: "c2", Name: "EDIT", Args: `{"path":"a.go","content":"new\n"}`}}},
		{content: "renamed the type"},
	}}
	exec := &fakeExec{results: map[string]textChange{
		"EDIT": {text: "[EDIT] proposed", chg: &Change{Path: "a.go", Orig: "old\n", New: "new\n"}},
	}}

	q := NewQueue(nil)
	task := q.EnqueueSub("rename the type", "chat")
	s := NewSubagent(prov, q, exec)
	s.Base = "/tmp/proj"

	res, err := s.Run(context.Background(), task)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Changes) != 1 || res.Changes[0].Path != "a.go" {
		t.Fatalf("changes = %+v", res.Changes)
	}
	// The changes must reach the queue, which is what puts the task in review.
	got, _ := q.Find(task.ID)
	if got.Status != StatusReview || len(got.Changes) != 1 {
		t.Fatalf("task = %+v, want review with one change", got)
	}
	if len(exec.calls) != 2 {
		t.Fatalf("tool calls = %v, want READ then EDIT", exec.calls)
	}
	// The sub-agent must not have RUN or any other tool: the executor decides.
	if len(prov.lastReq.Tools) != 2 {
		t.Fatalf("the request exposed %d tools, want the executor's two", len(prov.lastReq.Tools))
	}
}

func TestSubagentWithoutChangesFinishes(t *testing.T) {
	prov := &scriptedProvider{turns: []scriptedTurn{
		{calls: []ai.ToolCall{{ID: "c1", Name: "READ", Args: `{"arg":"a.go"}`}}},
		{content: "nothing to change"},
	}}
	exec := &fakeExec{}

	q := NewQueue(nil)
	task := q.EnqueueSub("look around", "")
	if _, err := NewSubagent(prov, q, exec).Run(context.Background(), task); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, _ := q.Find(task.ID)
	if got.Status != StatusDone {
		t.Fatalf("status = %q, want done (a sub-agent that changed nothing is a valid answer)", got.Status)
	}
	if got.Error != "" {
		t.Fatalf("error = %q", got.Error)
	}
}

// TestSubagentOneProposalPerPath: three edits (two to the same file) must reach
// the reviewer as two diffs, with the last edit winning — not as three
// conflicting hunks.
func TestSubagentOneProposalPerPath(t *testing.T) {
	prov := &scriptedProvider{turns: []scriptedTurn{
		{calls: []ai.ToolCall{
			{ID: "c1", Name: "EDIT", Args: `{"path":"a.go","content":"v2"}`},
			{ID: "c2", Name: "EDIT", Args: `{"path":"a.go","content":"v3"}`},
			{ID: "c3", Name: "EDIT", Args: `{"path":"b.go","content":"w"}`},
		}},
		{content: "done"},
	}}
	exec := &fakeExec{
		used: map[string]int{},
		seq: map[string][]textChange{
			"EDIT": {
				{text: "ok", chg: &Change{Path: "a.go", New: "v2"}},
				{text: "ok", chg: &Change{Path: "a.go", New: "v3"}},
				{text: "ok", chg: &Change{Path: "b.go", New: "w"}},
			},
		},
	}

	q := NewQueue(nil)
	task := q.EnqueueSub("edit", "")
	res, err := NewSubagent(prov, q, exec).Run(context.Background(), task)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Changes) != 2 {
		t.Fatalf("changes = %+v, want one per path", res.Changes)
	}
	if res.Changes[0].Path != "a.go" || res.Changes[0].New != "v3" {
		t.Fatalf("first path = %+v, want a.go at the last proposed content", res.Changes[0])
	}
	if res.Changes[1].Path != "b.go" {
		t.Fatalf("second path = %+v", res.Changes[1])
	}
}

func TestSubagentStopsAtRoundLimit(t *testing.T) {
	var turns []scriptedTurn
	for i := 0; i < 10; i++ {
		turns = append(turns, scriptedTurn{calls: []ai.ToolCall{{ID: "c", Name: "READ", Args: `{"arg":"a"}`}}})
	}
	prov := &scriptedProvider{turns: turns}
	exec := &fakeExec{}

	q := NewQueue(nil)
	task := q.EnqueueSub("loop forever", "")
	s := NewSubagent(prov, q, exec)
	s.Cfg.MaxRounds = 3

	if _, err := s.Run(context.Background(), task); err == nil {
		t.Fatal("an endless tool loop must stop with an error")
	}
	if len(exec.calls) != 3 {
		t.Fatalf("tool calls = %d, want the cap of 3", len(exec.calls))
	}
	got, _ := q.Find(task.ID)
	if got.Status == StatusRunning {
		t.Fatal("a task must never stay running after the loop ended")
	}
}

func TestSubagentTimeoutLeavesNoRunningTask(t *testing.T) {
	// A provider that blocks until the context dies: exactly the wedge that
	// would otherwise leave the chat parked forever.
	blocking := &blockingProvider{}
	q := NewQueue(nil)
	task := q.EnqueueSub("slow", "")
	s := NewSubagent(blocking, q, &fakeExec{})
	s.Cfg.Timeout = 30 * time.Millisecond

	if _, err := s.Run(context.Background(), task); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want a deadline error", err)
	}
	got, _ := q.Find(task.ID)
	if got.Status == StatusRunning {
		t.Fatal("a timed-out task must not stay running")
	}
}

type blockingProvider struct{}

func (b *blockingProvider) Models(context.Context) ([]string, error) { return nil, nil }
func (b *blockingProvider) ChatStream(ctx context.Context, _ ai.Request, _ ai.Handler) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestSubagentReportsProviderError(t *testing.T) {
	prov := &scriptedProvider{turns: []scriptedTurn{{err: errors.New("429 rate limited")}}}
	q := NewQueue(nil)
	task := q.EnqueueSub("x", "")

	if _, err := NewSubagent(prov, q, &fakeExec{}).Run(context.Background(), task); err == nil {
		t.Fatal("a provider error must surface")
	}
	got, _ := q.Find(task.ID)
	if got.Status != StatusFailed || !strings.Contains(got.Error, "429") {
		t.Fatalf("task = %+v, want a failure carrying the reason", got)
	}
}

func TestSubagentNeedsProviderAndTools(t *testing.T) {
	q := NewQueue(nil)
	task := q.EnqueueSub("x", "")
	if _, err := NewSubagent(nil, q, &fakeExec{}).Run(context.Background(), task); err == nil {
		t.Error("a sub-agent without a provider must fail loudly")
	}
	if _, err := NewSubagent(&scriptedProvider{}, q, nil).Run(context.Background(), task); err == nil {
		t.Error("a sub-agent without tools must fail loudly")
	}
}

func TestEnqueueSubMarksKindAndParent(t *testing.T) {
	q := NewQueue(nil)
	task := q.EnqueueSub("delegated", "chat:openai")
	if task.Kind != KindSubagent {
		t.Fatalf("kind = %q, want %q", task.Kind, KindSubagent)
	}
	if task.Parent != "chat:openai" {
		t.Fatalf("parent = %q", task.Parent)
	}
	main := q.Enqueue("hand started")
	if main.Kind != KindAgent {
		t.Fatalf("a hand-started task must keep the default kind, got %q", main.Kind)
	}
}
