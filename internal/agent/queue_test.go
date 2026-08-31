package agent

import (
	"sync"
	"testing"

	"dmed/internal/events"
)

type fakeBus struct {
	mu      sync.Mutex
	events  []events.Event
	updates []string
}

func (f *fakeBus) Publish(e events.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
	f.updates = append(f.updates, e.Path)
}

func (f *fakeBus) published() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.updates...)
}

func TestEnqueueAssignsIDsAndQueuedStatus(t *testing.T) {
	q := NewQueue(nil)
	a := q.Enqueue("refactor module x")
	b := q.Enqueue("fix bug y")

	if a.ID == b.ID {
		t.Fatalf("expected unique IDs, got %q and %q", a.ID, b.ID)
	}
	if a.Status != StatusQueued || b.Status != StatusQueued {
		t.Fatalf("expected queued status, got %s and %s", a.Status, b.Status)
	}
	if a.Progress != 0 {
		t.Fatalf("expected zero progress, got %v", a.Progress)
	}
}

func TestNextFIFOOrder(t *testing.T) {
	q := NewQueue(nil)
	q.Enqueue("first")
	q.Enqueue("second")
	q.Enqueue("third")

	first := q.Next()
	second := q.Next()
	third := q.Next()

	if first.Prompt != "first" || second.Prompt != "second" || third.Prompt != "third" {
		t.Fatalf("expected FIFO order, got %q, %q, %q", first.Prompt, second.Prompt, third.Prompt)
	}
	if first.Status != StatusRunning {
		t.Fatalf("expected running after Next, got %s", first.Status)
	}
	if got := q.Next(); got != nil {
		t.Fatalf("expected nil when queue empty, got %q", got.Prompt)
	}
}

func TestCancel(t *testing.T) {
	q := NewQueue(nil)
	t1 := q.Enqueue("task")
	if !q.Cancel(t1.ID) {
		t.Fatalf("expected cancel to succeed")
	}
	if t1.Status != StatusCancelled {
		t.Fatalf("expected cancelled status, got %s", t1.Status)
	}
	// A finished task cannot be cancelled again.
	if q.Cancel(t1.ID) {
		t.Fatalf("expected second cancel to fail")
	}
	// Cancelled task is never handed out by Next.
	if got := q.Next(); got != nil {
		t.Fatalf("expected nil Next after cancel, got %q", got.Prompt)
	}
}

func TestStatusTransitionsPublishToBus(t *testing.T) {
	bus := &fakeBus{}
	q := NewQueue(bus)

	t1 := q.Enqueue("task")
	updates := bus.published()
	if len(updates) != 1 {
		t.Fatalf("expected 1 publish on enqueue, got %d", len(updates))
	}

	// Terminal transitions are refused.
	q.SetStatus(t1.ID, StatusDone)
	if t1.Status != StatusDone {
		t.Fatalf("expected done, got %s", t1.Status)
	}
	q.SetStatus(t1.ID, StatusRunning)
	if t1.Status != StatusDone {
		t.Fatalf("terminal task should not change, got %s", t1.Status)
	}
}

func TestSetChangesMovesToReview(t *testing.T) {
	q := NewQueue(nil)
	t1 := q.Enqueue("task")
	changes := []Change{{Path: "a.go", Orig: "x", New: "y"}}
	q.SetChanges(t1.ID, changes)
	if t1.Status != StatusReview {
		t.Fatalf("expected review, got %s", t1.Status)
	}
	if len(t1.Changes) != 1 || t1.Changes[0].Path != "a.go" {
		t.Fatalf("changes not stored: %+v", t1.Changes)
	}
}

func TestSetErrorMarksFailed(t *testing.T) {
	q := NewQueue(nil)
	t1 := q.Enqueue("task")
	q.SetError(t1.ID, "boom")
	if t1.Status != StatusFailed || t1.Error != "boom" {
		t.Fatalf("expected failed with error, got %s %q", t1.Status, t1.Error)
	}
}

func TestSetProgressOnlyWhenRunning(t *testing.T) {
	q := NewQueue(nil)
	t1 := q.Enqueue("task")
	q.SetProgress(t1.ID, 0.5)
	if t1.Progress != 0 {
		t.Fatalf("progress should be ignored while queued, got %v", t1.Progress)
	}
	q.Next()
	q.SetProgress(t1.ID, 0.6)
	if t1.Progress != 0.6 {
		t.Fatalf("expected progress 0.6, got %v", t1.Progress)
	}
}

func TestSnapshotIsIndependent(t *testing.T) {
	q := NewQueue(nil)
	q.Enqueue("a")
	q.Enqueue("b")
	snap := q.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 tasks in snapshot, got %d", len(snap))
	}
}
