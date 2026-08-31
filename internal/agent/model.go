// Package agent implements background AI tasks that produce a series of
// changes. Following the project rule, agents never write to buffers
// directly: they emit Changes which pass through diff-review and an
// atomic apply step somewhere else.
package agent

import (
	"time"
)

// Status is the lifecycle state of a Task.
type Status string

const (
	StatusQueued    Status = "queued"    // waiting in the queue
	StatusRunning   Status = "running"   // agent goroutine executing
	StatusReview    Status = "review"    // changes produced, awaiting human review
	StatusApplied   Status = "applied"   // series applied atomically to buffers
	StatusDone      Status = "done"      // finished successfully
	StatusFailed    Status = "failed"    // errored during execution
	StatusCancelled Status = "cancelled" // aborted by the user
)

// Change is one proposed edit from an agent. It carries the original and
// proposed file content so a diff/preview can be rendered, and an optional
// context marker used to validate the patch still applies.
type Change struct {
	Path string // file path relative to the task/target
	Orig string // original content
	New  string // proposed content
}

// Task is a single background agent assignment.
type Task struct {
	ID       string
	Prompt   string
	Status   Status
	Progress float32 // 0..1; >0 only while running/streaming
	Changes  []Change
	Error    string
	Created  time.Time
	Updated  time.Time
}

// IsFinished reports whether the task reached a terminal state.
func (t *Task) IsFinished() bool {
	switch t.Status {
	case StatusDone, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

// Terminal reports whether no further work will occur (finished or applied).
func (t *Task) Terminal() bool {
	return t.IsFinished() || t.Status == StatusApplied
}
