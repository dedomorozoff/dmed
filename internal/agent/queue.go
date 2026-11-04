package agent

import (
	"strings"
	"sync"
	"time"

	"dmed/internal/ai"
	"dmed/internal/events"
)

// Queue is a thread-safe FIFO of agent Tasks. Every mutation publishes an
// EventAgentUpdated on the provided bus so the TUI can repaint, and Enqueue
// wakes the workers so they do not poll.
type Queue struct {
	mu    sync.Mutex
	tasks []Task // sorted oldest -> newest, includes finished tasks
	bus   publisher
	order uint64

	// One wake channel per registered worker. They are guarded by wakeMu, not
	// mu: notify runs while mu is held, and a worker registration must not
	// deadlock against an enqueue mid-flight.
	wakeMu sync.Mutex
	wakes  []chan struct{}
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

// Wake registers one worker and returns the channel it blocks on while the
// queue is empty: every Enqueue signals all registered workers, so a pool of
// workers drains a burst instead of one channel handing off one token. Each
// call registers a new channel — a caller asks once per worker goroutine.
// The wake is only a hint to call Next again, so buffered(1) per worker is
// enough and a coalesced burst loses no work.
func (q *Queue) Wake() <-chan struct{} {
	ch := make(chan struct{}, 1)
	q.wakeMu.Lock()
	q.wakes = append(q.wakes, ch)
	q.wakeMu.Unlock()
	return ch
}

func (q *Queue) notify() {
	q.wakeMu.Lock()
	defer q.wakeMu.Unlock()
	for _, ch := range q.wakes {
		select {
		case ch <- struct{}{}:
		default:
		}
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
	return q.enqueue(prompt, KindAgent, "")
}

// EnqueueSub appends a delegated sub-agent task. It goes through the same queue
// and the same review as a task the user started by hand, so a sub-agent cannot
// obtain write access the user would not have given a main agent.
func (q *Queue) EnqueueSub(prompt, parent string) Task {
	return q.enqueue(prompt, KindSubagent, parent)
}

func (q *Queue) enqueue(prompt string, kind TaskKind, parent string) Task {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now()
	q.order++
	t := Task{
		ID:      newID(q.order, now),
		Prompt:  prompt,
		Kind:    kind,
		Parent:  parent,
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

// SetUsage folds one token report into the task's total. It answers to no
// status check on purpose: the final usage chunk can arrive while the task
// is being moved into review. Reports add because every provider round
// re-sends (and re-bills) the context — a sub-agent with ten rounds pays ten
// prompts, and the card should say what the task cost, not what its last
// round did.
func (q *Queue) SetUsage(id string, u ai.Usage) {
	q.mu.Lock()
	defer q.mu.Unlock()
	i := q.findLocked(id)
	if i < 0 || u.Empty() {
		return
	}
	q.tasks[i].Usage = q.tasks[i].Usage.Add(u)
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

// Restore seeds the queue with tasks from a previous session (agent-panel
// persistence). An ID already present is skipped, and the order counter is
// advanced past every restored ID so a fresh Enqueue cannot collide with one.
// It reports how many tasks landed. The caller normalises interrupted states
// before restoring: the queue stores, it does not judge.
func (q *Queue) Restore(tasks []Task) int {
	q.mu.Lock()
	added := 0
	for _, t := range tasks {
		if q.findLocked(t.ID) >= 0 {
			continue
		}
		tt := t.clone()
		q.tasks = append(q.tasks, tt)
		if o := orderOfID(t.ID); o > q.order {
			q.order = o
		}
		q.publish(tt.ID)
		added++
	}
	q.mu.Unlock()
	if added > 0 {
		q.notify()
	}
	return added
}

// orderOfID extracts the counter embedded by newID ("t<date>-<order>"); an
// ID from an older format yields 0 and simply does not move the counter.
func orderOfID(id string) uint64 {
	i := strings.LastIndexByte(id, '-')
	if i < 0 || i == len(id)-1 {
		return 0
	}
	var n uint64
	for _, c := range id[i+1:] {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + uint64(c-'0')
	}
	return n
}

// newID builds a short, unique, sortable task ID. The date is part of it
// because IDs survive the session: a restored task and a fresh one from the
// same clock minute must not share an address, and the panel, the revert
// trail and the event bus all name a task by it.
func newID(order uint64, now time.Time) string {
	return "t" + now.Format("0102-150405") + "-" + itoa(order)
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
