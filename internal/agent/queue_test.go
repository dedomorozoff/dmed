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

	first, ok := q.Next()
	if !ok {
		t.Fatal("first Next must succeed")
	}
	second, _ := q.Next()
	third, _ := q.Next()

	if first.Prompt != "first" || second.Prompt != "second" || third.Prompt != "third" {
		t.Fatalf("expected FIFO order, got %q, %q, %q", first.Prompt, second.Prompt, third.Prompt)
	}
	if first.Status != StatusRunning {
		t.Fatalf("expected running after Next, got %s", first.Status)
	}
	if got, ok := q.Next(); ok {
		t.Fatalf("expected no task when queue empty, got %q", got.Prompt)
	}
}

func TestCancel(t *testing.T) {
	q := NewQueue(nil)
	t1 := q.Enqueue("task")
	if !q.Cancel(t1.ID) {
		t.Fatalf("expected cancel to succeed")
	}
	cur, _ := q.Find(t1.ID)
	if cur.Status != StatusCancelled {
		t.Fatalf("expected cancelled status, got %s", cur.Status)
	}
	// A finished task cannot be cancelled again.
	if q.Cancel(t1.ID) {
		t.Fatalf("expected second cancel to fail")
	}
	// Cancelled task is never handed out by Next.
	if got, ok := q.Next(); ok {
		t.Fatalf("expected no task after cancel, got %q", got.Prompt)
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
	cur, _ := q.Find(t1.ID)
	if cur.Status != StatusDone {
		t.Fatalf("expected done, got %s", cur.Status)
	}
	q.SetStatus(t1.ID, StatusRunning)
	cur, _ = q.Find(t1.ID)
	if cur.Status != StatusDone {
		t.Fatalf("terminal task should not change, got %s", cur.Status)
	}
}

func TestSetChangesMovesToReview(t *testing.T) {
	q := NewQueue(nil)
	t1 := q.Enqueue("task")
	changes := []Change{{Path: "a.go", Orig: "x", New: "y"}}
	q.SetChanges(t1.ID, changes)
	cur, _ := q.Find(t1.ID)
	if cur.Status != StatusReview {
		t.Fatalf("expected review, got %s", cur.Status)
	}
	if len(cur.Changes) != 1 || cur.Changes[0].Path != "a.go" {
		t.Fatalf("changes not stored: %+v", cur.Changes)
	}
	// The queue must own its slice: a later write through the caller's
	// variable cannot reach into stored state.
	changes[0].Path = "mutated.go"
	cur, _ = q.Find(t1.ID)
	if cur.Changes[0].Path != "a.go" {
		t.Fatalf("stored changes aliased the caller's slice: %+v", cur.Changes)
	}
}

func TestSetErrorMarksFailed(t *testing.T) {
	q := NewQueue(nil)
	t1 := q.Enqueue("task")
	q.SetError(t1.ID, "boom")
	cur, _ := q.Find(t1.ID)
	if cur.Status != StatusFailed || cur.Error != "boom" {
		t.Fatalf("expected failed with error, got %s %q", cur.Status, cur.Error)
	}
}

func TestSetProgressOnlyWhenRunning(t *testing.T) {
	q := NewQueue(nil)
	t1 := q.Enqueue("task")
	q.SetProgress(t1.ID, 0.5)
	cur, _ := q.Find(t1.ID)
	if cur.Progress != 0 {
		t.Fatalf("progress should be ignored while queued, got %v", cur.Progress)
	}
	q.Next()
	q.SetProgress(t1.ID, 0.6)
	cur, _ = q.Find(t1.ID)
	if cur.Progress != 0.6 {
		t.Fatalf("expected progress 0.6, got %v", cur.Progress)
	}
}

// TestSnapshotIsIndependent pins the invariant that makes the TUI race-free:
// the queue hands out copies, so a caller can never observe — or mutate —
// queue state through a snapshot taken while the runner is updating tasks.
func TestSnapshotIsIndependent(t *testing.T) {
	q := NewQueue(nil)
	q.Enqueue("a")
	q.Enqueue("b")
	snap := q.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 tasks in snapshot, got %d", len(snap))
	}

	// Mutating the snapshot must not touch the queue.
	snap[0].Prompt = "mutated"
	snap[0].Status = StatusFailed
	fresh := q.Snapshot()
	if fresh[0].Prompt != "a" || fresh[0].Status != StatusQueued {
		t.Fatalf("snapshot aliases queue storage: %+v", fresh[0])
	}

	// Queue transitions must not be visible through a snapshot already taken.
	before := q.Snapshot()
	q.Next()
	after := q.Snapshot()
	if before[0].Status != StatusQueued {
		t.Fatalf("old snapshot changed under the caller: %+v", before[0])
	}
	if after[0].Status != StatusRunning {
		t.Fatalf("queue did not transition to running: %+v", after[0])
	}
}

func TestFindMissingReportsNotFound(t *testing.T) {
	q := NewQueue(nil)
	q.Enqueue("a")
	if _, ok := q.Find("nope"); ok {
		t.Fatal("Find must report false for an unknown id")
	}
}

// TestQueueConcurrentReadWrite is the regression test for the data race that
// the audit found: the queue used to hand out *Task pointers, so the TUI read
// fields the runner goroutine was writing. Under -race this fails without the
// copy-on-read invariant.
func TestQueueConcurrentReadWrite(t *testing.T) {
	q := NewQueue(nil)
	id := q.Enqueue("work").ID
	q.Next()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			q.SetProgress(id, float32(i%100)/100)
			q.SetStatus(id, StatusRunning)
		}
	}()

	for i := 0; i < 500; i++ {
		if tk, ok := q.Find(id); ok {
			_ = tk.Progress
			_ = tk.Status
			_ = tk.Changes
		}
		_ = q.Snapshot()
	}
	<-done
}

func TestWakeSignalsOnEnqueue(t *testing.T) {
	q := NewQueue(nil)
	// Drain any stale token so the check below starts clean.
	select {
	case <-q.Wake():
	default:
	}
	select {
	case <-q.Wake():
		t.Fatalf("wake should not be signaled before any enqueue")
	default:
	}
	q.Enqueue("a")
	select {
	case <-q.Wake():
	default:
		t.Fatalf("expected a wake signal after first enqueue")
	}
	q.Enqueue("b")
	select {
	case <-q.Wake():
	default:
		t.Fatalf("expected a wake signal after second enqueue")
	}
}
