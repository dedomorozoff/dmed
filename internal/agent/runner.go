package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"dmed/internal/ai"
)

// Runner executes queued tasks against an LLM provider, streaming progress
// and storing the produced changes for human review. It never touches buffers
// or files directly — only produces Change objects.
type Runner struct {
	prov  ai.Provider
	queue *Queue
	// Prompt builds the system/user message for a task; defaults to a
	// built-in prompt if nil.
	Prompt func(prompt string, files []TargetFile) ([]ai.Message, error)
	// ReadTarget returns the content of a target file to use as Orig.
	// Defaults to reading from disk relative to a base directory.
	ReadTarget func(f TargetFile) (string, error)

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

// TargetFile is a file passed to the agent as input context.
type TargetFile struct {
	Path    string // logical/repo-relative path
	Content string // current content
}

// NewRunner creates a Runner bound to a queue.
func NewRunner(prov ai.Provider, queue *Queue) *Runner {
	return &Runner{
		prov:    prov,
		queue:   queue,
		cancels: make(map[string]context.CancelFunc),
	}
}

// Cancel requests cancellation of a task. A task that is still queued is
// marked cancelled immediately; a running task has its LLM call stopped
// (state flips when the Run loop observes ctx cancellation).
func (r *Runner) Cancel(id string) {
	r.mu.Lock()
	cancel := r.cancels[id]
	r.mu.Unlock()
	if cancel != nil {
		cancel()
		return
	}
	// Not currently registered: if it was only queued, mark it cancelled now.
	if r.queue != nil {
		r.queue.Cancel(id)
	}
}

// Run executes one task. It blocks until the task is done, failed, or
// cancelled. Callers should run this in a goroutine. parent is the base
// context (e.g. the editor lifetime).
func (r *Runner) Run(parent context.Context, task *Task, targets []TargetFile) {
	if r.prov == nil {
		r.queue.SetError(task.ID, "no AI provider configured")
		return
	}

	ctx, cancel := context.WithCancel(parent)
	r.mu.Lock()
	r.cancels[task.ID] = cancel
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.cancels, task.ID)
		r.mu.Unlock()
		cancel()
	}()

	msgs, err := r.buildMessages(task.Prompt, targets)
	if err != nil {
		r.queue.SetError(task.ID, err.Error())
		return
	}

	ch := make(chan runEvent, 64)
	go r.stream(ctx, task.ID, msgs, ch)

	var sb strings.Builder
	var bytes int
	for {
		select {
		case <-ctx.Done():
			r.queue.SetStatus(task.ID, StatusCancelled)
			return
		case ev, ok := <-ch:
			if !ok {
				if ctx.Err() != nil {
					r.queue.SetStatus(task.ID, StatusCancelled)
					return
				}
				r.finish(task.ID, sb.String(), targets)
				return
			}
			if ev.err != nil {
				if ctx.Err() != nil {
					r.queue.SetStatus(task.ID, StatusCancelled)
					return
				}
				r.queue.SetError(task.ID, ev.err.Error())
				return
			}
			sb.WriteString(ev.delta)
			bytes += len(ev.delta)
			r.queue.SetProgress(task.ID, progressEstimate(bytes))
		}
	}
}

// progressEstimate maps the number of streamed bytes to a 0..1 estimate.
// There is no known total, so it approaches 0.9 asymptotically; the final
// hop to 1 happens when changes land (review).
func progressEstimate(bytes int) float32 {
	x := float32(bytes) / 1024 // per ~KiB
	if x < 0 {
		x = 0
	}
	// 1 - 1/(x+1) -> 0..1; cap at 0.9 while streaming.
	p := 1 - 1/(x+1)
	if p > 0.9 {
		p = 0.9
	}
	return p
}

func (r *Runner) buildMessages(prompt string, targets []TargetFile) ([]ai.Message, error) {
	if r.Prompt != nil {
		return r.Prompt(prompt, targets)
	}
	var b strings.Builder
	b.WriteString("You are a refactoring assistant integrated into a code editor. ")
	b.WriteString("The user gives a task over the files below. ")
	b.WriteString("Produce the FULL new content of every file you change.\n\n")
	b.WriteString("Format each changed file exactly like this:\n\n")
	b.WriteString("=== FILE: <relative-path> ===\n")
	b.WriteString("<complete new file content, no other decoration>\n\n")
	b.WriteString("Only include files you actually change. Preserve unrelated content. ")
	b.WriteString("Do not wrap in markdown fences; do not add commentary inside blocks.\n\n")

	if len(targets) == 0 {
		b.WriteString("No input files provided.\n")
	} else {
		for _, f := range targets {
			fmt.Fprintf(&b, "--- FILE %s ---\n%s\n", f.Path, f.Content)
		}
	}

	user := fmt.Sprintf("Task: %s", prompt)
	return []ai.Message{
		{Role: "system", Content: b.String()},
		{Role: "user", Content: user},
	}, nil
}

func (r *Runner) stream(ctx context.Context, id string, msgs []ai.Message, ch chan<- runEvent) {
	defer close(ch)
	err := r.prov.ChatStream(ctx, msgs, func(d string) {
		select {
		case ch <- runEvent{delta: d}:
		case <-ctx.Done():
		}
	})
	if err != nil {
		select {
		case ch <- runEvent{err: err}:
		case <-ctx.Done():
		}
	}
}

// finish parses the response, fills Orig from disk, and moves the task to review.
func (r *Runner) finish(id, response string, targets []TargetFile) {
	changes, err := parseBlocks(response)
	if err != nil {
		r.queue.SetError(id, err.Error())
		return
	}
	for i := range changes {
		orig, rerr := r.origFor(changes[i].Path, targets)
		if rerr != nil {
			r.queue.SetError(id, rerr.Error())
			return
		}
		changes[i].Orig = orig
	}
	r.queue.SetChanges(id, changes)
}

func (r *Runner) origFor(path string, targets []TargetFile) (string, error) {
	if r.ReadTarget != nil {
		return r.ReadTarget(TargetFile{Path: path})
	}
	for _, t := range targets {
		if t.Path == path {
			return t.Content, nil
		}
	}
	// Fall back to reading the file directly.
	data, err := os.ReadFile(filepath.FromSlash(path))
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", path, err)
	}
	return string(data), nil
}

// runEvent is an internal streaming message from the provider goroutine.
type runEvent struct {
	delta string
	err   error
}
