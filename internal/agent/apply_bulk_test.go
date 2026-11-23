package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestApplyBulkNewProject is the "agent generated a whole project" case:
// hundreds of new files plus edits to existing ones travel in one Change
// series, exactly as a reviewed agent proposal does. Applier validates the
// whole batch before writing any of it and writes it as one atomic step.
func TestApplyBulkNewProject(t *testing.T) {
	const (
		created = 300
		edited  = 100
	)
	dir := t.TempDir()
	a := NewApplier()
	a.Root = dir

	changes := make([]Change, 0, created+edited)
	for i := 0; i < edited; i++ {
		rel := fmt.Sprintf("src/existing%03d.go", i)
		orig := fmt.Sprintf("// original %d\n", i)
		writeTestFile(t, dir, rel, orig)
		changes = append(changes, Change{Path: rel, Orig: orig, New: fmt.Sprintf("// generated %d\n", i)})
	}
	for i := 0; i < created; i++ {
		rel := fmt.Sprintf("gen/pkg%02d/file%03d.go", i/50, i)
		changes = append(changes, Change{Path: rel, Orig: "", New: fmt.Sprintf("package pkg%02d\n// file %d\n", i/50, i)})
	}

	start := time.Now()
	if err := a.Apply(changes); err != nil {
		t.Fatalf("bulk apply failed: %v", err)
	}
	t.Logf("applied %d changes (%d new files) in %v", len(changes), created, time.Since(start))

	for i, c := range changes {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(c.Path)))
		if err != nil {
			t.Fatalf("change %d: read %s: %v", i, c.Path, err)
		}
		if string(got) != c.New {
			t.Fatalf("change %d: %s = %q, want %q", i, c.Path, got, c.New)
		}
	}

	// One stale change must reject the whole series without touching a single
	// file, even when the other 799 changes are perfectly applicable.
	changes[created/2].Orig = "the file moved on\n"
	if err := a.Apply(changes); err == nil {
		t.Fatal("a stale change must reject the entire bulk series")
	}
	for i, c := range changes {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(c.Path)))
		if err != nil {
			t.Fatalf("change %d: after rejected apply: read %s: %v", i, c.Path, err)
		}
		if string(got) != c.New {
			t.Fatalf("change %d: rejected series wrote %s: %q", i, c.Path, got)
		}
	}
}

// TestApplyBulkRollbackOnPartialFailure pushes a write failure into the middle
// of a long series: every earlier file must be restored and every new file
// created before the failure must be removed.
func TestApplyBulkRollbackOnPartialFailure(t *testing.T) {
	const n = 150
	dir := t.TempDir()
	a := NewApplier()
	a.Root = dir

	changes := make([]Change, 0, n)
	for i := 0; i < n; i++ {
		rel := fmt.Sprintf("src/file%03d.go", i)
		orig := fmt.Sprintf("original %d\n", i)
		writeTestFile(t, dir, rel, orig)
		changes = append(changes, Change{Path: rel, Orig: orig, New: fmt.Sprintf("generated %d\n", i)})
	}

	var writes int
	a.Write = func(p, content string) error {
		writes++
		if writes == n/2 {
			return errors.New("disk full")
		}
		p = filepath.FromSlash(p)
		if d := filepath.Dir(p); d != "" && d != "." {
			if err := os.MkdirAll(d, 0o755); err != nil {
				return err
			}
		}
		return os.WriteFile(p, []byte(content), 0o644)
	}

	if err := a.Apply(changes); err == nil {
		t.Fatal("expected the injected write failure")
	}
	for i, c := range changes {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(c.Path)))
		if err != nil {
			t.Fatalf("change %d: after rollback: read %s: %v", i, c.Path, err)
		}
		if string(got) != c.Orig {
			t.Fatalf("change %d: rollback left %s = %q, want %q", i, c.Path, got, c.Orig)
		}
	}
}

func writeTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}
