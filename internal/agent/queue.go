package agent

import (
	"sync"
	"time"

	"dmed/internal/events"
)

// Queue is a thread-safe FIFO of agent Tasks. Every mutation publishes an
// EventAgentUpdated on the provided bus so the TUI can repaint, and Enqueue
// wakes the worker channel so it does not poll.
type Queue struct {
	mu    sync.Mutex
	tasks []Task // sorted oldest -> newest, includes finished tasks
	bus   publisher
	wake  chan struct{} // buffered(1); signaled when a task is enqueued
	order uint64
}

// events.Publisher is the subset of the event bus the queue needs, so the
// agent package stays easy to test with a fake.
type publisher interface {
	Publish(events.Event)
}

// NewQueue creates an empty queue. bus may be nil (publishing becomes a no-op).
func NewQueue(bus publisher) *Queue {
	return &Queue{bus: bus, wake: make(chan struct{}, 1)}
}

// Wake returns the channel that is signaled whenever a task is enqueued.
// A blocked worker drains it and re-checks the queue; a buffered(1) channel is
// enough for a single worker because the wake is only a hint to call Next.
func (q *Queue) Wake() <-chan struct{} { return q.wake }

func (q *Queue) notify() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *Queue) publish(id string) {
	if q.bus == nil {
		return
	}
	q.bus.Publish(events.Event{Type: events.EventAgentUpdated, Path: id})
}

// Enqueue appends a new task in queued state and returns a copy of it.
// The task ID is auto-generated.
func (q *Queue) Enqueue(prompt string) Task {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now()
	q.order++
	t := Task{
		ID:      newID(q.order, now),
		Prompt:  prompt,
		Status:  StatusQueued,
		Created: now,
		Updated: now,
	}
	q.tasks = append(q.tasks, t)
	q.publish(t.ID)
	q.notify()
	return t
}

// Next removes and returns the oldest queued task, marking it running.
// It reports false if there is nothing to run.
//
// The returned Task is a copy: the queue keeps sole ownership of its own
// storage, so the caller may read it freely while the queue is being mutated
// by the runner goroutine. This is what keeps the TUI race-free.
func (q *Queue) Next() (Task, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for i, t := range q.tasks {
		if t.Status == StatusQueued {
			t.Status = StatusRunning
			t.Updated = time.Now()
			q.tasks[i] = t
			q.publish(t.ID)
			return t, true
		}
	}
	return Task{}, false
}

// Find returns a copy of the task with the given ID, and whether it exists.
// The copy is taken under the lock, so every field is read consistently even
// while the runner goroutine is updating the task.
func (q *Queue) Find(id string) (Task, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, t := range q.tasks {
		if t.ID == id {
			return t.clone(), true
		}
	}
	return Task{}, false
}

// Snapshot returns copies of every task, oldest first. Callers may read and
// keep the result; it never aliases the queue's own storage.
func (q *Queue) Snapshot() []Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Task, len(q.tasks))
	for i, t := range q.tasks {
		out[i] = t.clone()
	}
	return out
}

// SetStatus transitions a task to a new status and publishes an update.
// It is a no-op if the task does not exist or is already finished/terminal.
func (q *Queue) SetStatus(id string, s Status) {
	q.mu.Lock()
	defer q.mu.Unlock()
	i := q.findLocked(id)
	if i < 0 || q.tasks[i].Terminal() {
		return
	}
	q.tasks[i].Status = s
	q.tasks[i].Updated = time.Now()
	q.publish(id)
}

// SetError records a failure message and marks the task failed.
func (q *Queue) SetError(id, msg string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	i := q.findLocked(id)
	if i < 0 || q.tasks[i].Terminal() {
		return
	}
	q.tasks[i].Status = StatusFailed
	q.tasks[i].Error = msg
	q.tasks[i].Updated = time.Now()
	q.publish(id)
}

// SetProgress updates the progress of a running task.
func (q *Queue) SetProgress(id string, p float32) {
	q.mu.Lock()
	defer q.mu.Unlock()
	i := q.findLocked(id)
	if i < 0 || q.tasks[i].Status != StatusRunning {
		return
	}
	q.tasks[i].Progress = p
	q.tasks[i].Updated = time.Now()
	q.publish(id)
}

// SetChanges stores the produced changes and moves the task into review.
// The slice is copied so a later reuse of the caller's slice cannot mutate
// what the queue holds.
func (q *Queue) SetChanges(id string, changes []Change) {
	q.mu.Lock()
	defer q.mu.Unlock()
	i := q.findLocked(id)
	if i < 0 || q.tasks[i].Terminal() {
		return
	}
	own := make([]Change, len(changes))
	copy(own, changes)
	q.tasks[i].Changes = own
	q.tasks[i].Status = StatusReview
	q.tasks[i].Updated = time.Now()
	q.publish(id)
}

// Cancel aborts a queued or running task (third-party coordinators cancel the
// underlying context; this only marks state). Returns whether it was cancelled.
func (q *Queue) Cancel(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	i := q.findLocked(id)
	if i < 0 || q.tasks[i].Terminal() {
		return false
	}
	q.tasks[i].Status = StatusCancelled
	q.tasks[i].Updated = time.Now()
	q.publish(id)
	return true
}

// findLocked returns the index of the task with the given ID, or -1.
func (q *Queue) findLocked(id string) int {
	for i, t := range q.tasks {
		if t.ID == id {
			return i
		}
	}
	return -1
}

// newID builds a short, unique, sortable task ID.
func newID(order uint64, now time.Time) string {
	return "t" + now.Format("150405") + "-" + itoa(order)
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
