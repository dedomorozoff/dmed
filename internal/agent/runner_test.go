package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dmed/internal/ai"
)

func TestParseBlocks(t *testing.T) {
	text := `Some preamble note here.

=== FILE: src/a.go ===
package a

func A() {}

=== FILE: src/b.go ===

package b
func B() {}
`

	changes, err := parseBlocks(text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("expected 2 changes, got %d", len(changes))
	}
	if changes[0].Path != "src/a.go" {
		t.Fatalf("path = %q", changes[0].Path)
	}
	if !strings.Contains(changes[0].New, "func A() {}") {
		t.Fatalf("missing content: %q", changes[0].New)
	}
	if changes[1].Path != "src/b.go" {
		t.Fatalf("path = %q", changes[1].Path)
	}
	if !strings.Contains(changes[1].New, "func B() {}") {
		t.Fatalf("missing content: %q", changes[1].New)
	}
}

func TestParseBlocksNoHeader(t *testing.T) {
	if _, err := parseBlocks("just some prose without headers"); err == nil {
		t.Fatalf("expected error when no FILE blocks present")
	}
}

func TestParseBlocksEmptyPath(t *testing.T) {
	if _, err := parseBlocks("=== FILE: ===\ncontent"); err == nil {
		t.Fatalf("expected error for empty path header")
	}
}

type fakeProvider struct {
	delta string
	err   error
	// block makes ChatStream wait for ctx.Done instead of streaming,
	// simulating a long in-flight call.
	block   bool
	started chan struct{} // closed when ChatStream is invoked
}

func (f *fakeProvider) Models(ctx context.Context) ([]string, error) { return nil, nil }
func (f *fakeProvider) ChatStream(ctx context.Context, _ ai.Request, h ai.Handler) error {
	if f.started != nil {
		close(f.started)
	}
	if f.block {
		<-ctx.Done()
		return ctx.Err()
	}
	for _, d := range f.delta {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if h.Delta != nil {
				h.Delta(string(d))
			}
		}
	}
	return f.err
}

func TestRunnerProducesChanges(t *testing.T) {
	prov := &fakeProvider{delta: "=== FILE: a.go ===\npackage x\n"}
	q := NewQueue(nil)
	r := NewRunner(prov, q)

	task := q.Enqueue("refactor")
	q.Next() // -> running
	go r.Run(context.Background(), task, []TargetFile{{Path: "a.go", Content: "package old\n"}})

	waitStatus(t, q, task.ID, StatusReview)
	if len(task.Changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(task.Changes))
	}
	if task.Changes[0].Path != "a.go" {
		t.Fatalf("path = %q", task.Changes[0].Path)
	}
	if !strings.Contains(task.Changes[0].New, "package x") {
		t.Fatalf("new content = %q", task.Changes[0].New)
	}
	if !strings.Contains(task.Changes[0].Orig, "package old") {
		t.Fatalf("orig (from target) = %q", task.Changes[0].Orig)
	}
	if task.Status != StatusReview {
		t.Fatalf("status = %s, want review", task.Status)
	}
}

func TestRunnerErrorMarksFailed(t *testing.T) {
	prov := &fakeProvider{err: errors.New("boom")}
	q := NewQueue(nil)
	r := NewRunner(prov, q)

	task := q.Enqueue("job")
	q.Next()
	go r.Run(context.Background(), task, nil)

	waitStatus(t, q, task.ID, StatusFailed)
	if task.Error != "boom" {
		t.Fatalf("error = %q", task.Error)
	}
}

func TestRunnerCancellation(t *testing.T) {
	started := make(chan struct{})
	prov := &fakeProvider{block: true, started: started}
	q := NewQueue(nil)
	r := NewRunner(prov, q)

	task := q.Enqueue("job")
	q.Next()
	go r.Run(context.Background(), task, []TargetFile{{Path: "a.go", Content: "old"}})

	// Wait until the provider call is in flight (and the cancel func is
	// registered), then cancel.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatalf("provider call did not start")
	}

	r.Cancel(task.ID)
	waitStatus(t, q, task.ID, StatusCancelled)
}

func TestProgressGrowsWithBytes(t *testing.T) {
	if progressEstimate(0) != 0 {
		t.Fatalf("progress at 0 bytes should be 0")
	}
	small := progressEstimate(1024)
	large := progressEstimate(1 << 20)
	if !(small > 0 && small <= 0.9) {
		t.Fatalf("small = %v", small)
	}
	if !(large > small && large <= 0.9) {
		t.Fatalf("large = %v, small = %v", large, small)
	}
}

func TestOrigForMatchesNormalizedPaths(t *testing.T) {
	prov := &fakeProvider{delta: ""}
	q := NewQueue(nil)
	r := NewRunner(prov, q)
	targets := []TargetFile{
		{Path: "src/a.go", Content: "one\n"},
		{Path: "src/b.go", Content: "two\n"},
	}
	// A leading "./" and backslashes from the model must still match targets.
	got, err := r.origFor("./src/a.go", targets)
	if err != nil || got != "one\n" {
		t.Fatalf("a.go: got %q err=%v", got, err)
	}
	got, err = r.origFor("src\\b.go", targets)
	if err != nil || got != "two\n" {
		t.Fatalf("b.go: got %q err=%v", got, err)
	}
}

func TestOrigForResolvesAgainstBase(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(p, []byte("disc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prov := &fakeProvider{delta: ""}
	q := NewQueue(nil)
	r := NewRunner(prov, q)
	r.Base = dir
	got, err := r.origFor("x.txt", nil)
	if err != nil || got != "disc\n" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func waitStatus(t *testing.T, q *Queue, id string, want Status) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if tk := q.Find(id); tk != nil && tk.Status == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	tk := q.Find(id)
	if tk == nil {
		t.Fatalf("task %s not found (want %s)", id, want)
	}
	t.Fatalf("task %s status = %s, want %s", id, tk.Status, want)
}
