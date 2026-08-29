package agent

import (
	"sync"
	"time"

	"dmed/internal/events"
)

// Queue is a thread-safe FIFO of agent Tasks. Every mutation publishes an
// EventAgentUpdated on the provided bus so the TUI can repaint.
type Queue struct {
	mu    sync.Mutex
	tasks []*Task // sorted oldest -> newest, includes finished tasks
	bus   publisher
	order uint64
}

// events.Publisher is the subset of the event bus the queue needs, so the
// agent package stays easy to test with a fake.
type publisher interface {
	Publish(events.Event)
}

// NewQueue creates an empty queue. bus may be nil (publishing becomes a no-op).
func NewQueue(bus publisher) *Queue {
	return &Queue{bus: bus}
}

func (q *Queue) publish(id string) {
	if q.bus == nil {
		return
	}
	q.bus.Publish(events.Event{Type: events.EventAgentUpdated, Path: id})
}

// Enqueue appends a new task in queued state and returns it.
// The task ID is auto-generated if empty.
func (q *Queue) Enqueue(prompt string) *Task {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now()
	q.order++
	t := &Task{
		ID:      newID(q.order, now),
		Prompt:  prompt,
		Status:  StatusQueued,
		Created: now,
		Updated: now,
	}
	q.tasks = append(q.tasks, t)
	q.publish(t.ID)
	return t
}

// Next removes and returns the oldest queued task, marking it running.
// It returns nil if there is nothing to run.
func (q *Queue) Next() *Task {
	q.mu.Lock()
	defer q.mu.Unlock()

	for _, t := range q.tasks {
		if t.Status == StatusQueued {
			t.Status = StatusRunning
			t.Updated = time.Now()
			q.publish(t.ID)
			return t
		}
	}
	return nil
}

// Find returns the task with the given ID, or nil.
func (q *Queue) Find(id string) *Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, t := range q.tasks {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// Snapshot returns a copy of the task list (shared pointers, but callers
// should treat it read-only).
func (q *Queue) Snapshot() []*Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]*Task, len(q.tasks))
	copy(out, q.tasks)
	return out
}

// SetStatus transitions a task to a new status and publishes an update.
// It is a no-op if the task does not exist or is already finished/terminal.
func (q *Queue) SetStatus(id string, s Status) {
	q.mu.Lock()
	defer q.mu.Unlock()
	t := q.findLocked(id)
	if t == nil || t.Terminal() {
		return
	}
	t.Status = s
	t.Updated = time.Now()
	q.publish(t.ID)
}

// SetError records a failure message and marks the task failed.
func (q *Queue) SetError(id, msg string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	t := q.findLocked(id)
	if t == nil || t.Terminal() {
		return
	}
	t.Status = StatusFailed
	t.Error = msg
	t.Updated = time.Now()
	q.publish(t.ID)
}

// SetProgress updates the progress of a running task.
func (q *Queue) SetProgress(id string, p float32) {
	q.mu.Lock()
	defer q.mu.Unlock()
	t := q.findLocked(id)
	if t == nil || t.Status != StatusRunning {
		return
	}
	t.Progress = p
	t.Updated = time.Now()
	q.publish(t.ID)
}

// SetChanges stores the produced changes and moves the task into review.
func (q *Queue) SetChanges(id string, changes []Change) {
	q.mu.Lock()
	defer q.mu.Unlock()
	t := q.findLocked(id)
	if t == nil || t.Terminal() {
		return
	}
	t.Changes = changes
	t.Status = StatusReview
	t.Updated = time.Now()
	q.publish(t.ID)
}

// Cancel aborts a queued or running task (third-party coordinators cancel the
// underlying context; this only marks state). Returns whether it was cancelled.
func (q *Queue) Cancel(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	t := q.findLocked(id)
	if t == nil || t.Terminal() {
		return false
	}
	t.Status = StatusCancelled
	t.Updated = time.Now()
	q.publish(t.ID)
	return true
}

func (q *Queue) findLocked(id string) *Task {
	for _, t := range q.tasks {
		if t.ID == id {
			return t
		}
	}
	return nil
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
