package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplySuccess(t *testing.T) {
	fs := map[string]string{
		"a.go": "package old\n",
		"b.go": "one\ntwo\n",
	}
	a := NewApplier()
	a.Read = func(p string) (string, error) { return fs[p], nil }
	a.Write = func(p, c string) error { fs[p] = c; return nil }

	changes := []Change{
		{Path: "a.go", Orig: "package old\n", New: "package new\n"},
		{Path: "b.go", Orig: "one\ntwo\n", New: "one\nthree\n"},
	}
	if err := a.Apply(changes); err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if fs["a.go"] != "package new\n" {
		t.Fatalf("a.go not written: %q", fs["a.go"])
	}
	if fs["b.go"] != "one\nthree\n" {
		t.Fatalf("b.go not written: %q", fs["b.go"])
	}
}

func TestApplyRejectsStaleWithoutWriting(t *testing.T) {
	fs := map[string]string{"a.go": "changed-later\n"}
	a := NewApplier()
	a.Read = func(p string) (string, error) { return fs[p], nil }
	a.Write = func(p, c string) error { fs[p] = c; return nil }

	changes := []Change{
		{Path: "a.go", Orig: "old-snapshot\n", New: "new\n"},
	}
	if err := a.Apply(changes); err == nil {
		t.Fatalf("expected stale-patch rejection")
	}
	if fs["a.go"] != "changed-later\n" {
		t.Fatalf("file should be untouched, got %q", fs["a.go"])
	}
}

func TestApplyRollsBackOnPartialFailure(t *testing.T) {
	writes := map[string]bool{}
	fs := map[string]string{
		"a.go": "AAA\n",
		"b.go": "BBB\n",
	}
	a := NewApplier()
	a.Read = func(p string) (string, error) { return fs[p], nil }
	a.Write = func(p, c string) error {
		writes[p] = true
		if p == "b.go" {
			return errors.New("disk full")
		}
		fs[p] = c
		return nil
	}

	changes := []Change{
		{Path: "a.go", Orig: "AAA\n", New: "new-A\n"},
		{Path: "b.go", Orig: "BBB\n", New: "new-B\n"},
	}
	if err := a.Apply(changes); err == nil {
		t.Fatalf("expected write failure")
	}
	// a.go must be rolled back to original.
	if fs["a.go"] != "AAA\n" {
		t.Fatalf("a.go not rolled back: %q", fs["a.go"])
	}
	// b.go must not have changed (its write failed).
	if fs["b.go"] != "BBB\n" {
		t.Fatalf("b.go unexpected: %q", fs["b.go"])
	}
	if !writes["a.go"] || !writes["b.go"] {
		t.Fatalf("expected both write attempts, got %v", writes)
	}
}

func TestValidateDetectsConflict(t *testing.T) {
	fs := map[string]string{"a.go": "current\n"}
	a := NewApplier()
	a.Read = func(p string) (string, error) { return fs[p], nil }
	a.Write = func(p, c string) error { fs[p] = c; return nil }

	changes := []Change{{Path: "a.go", Orig: "stale\n", New: "new\n"}}
	if err := a.Validate(changes); err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestValidateEmptyPath(t *testing.T) {
	a := NewApplier()
	a.Read = func(p string) (string, error) { return "", nil }
	if err := a.Validate([]Change{{Path: "", Orig: "x", New: "y"}}); err == nil {
		t.Fatalf("expected error for empty path")
	}
}
// TestApplyCreatesMissingFile covers the AI "create a new file" flow: a Change
// with empty Orig for a file that does not exist is a creation, and nested
// directories are created as needed.
func TestApplyCreatesMissingFile(t *testing.T) {
	dir := filepath.FromSlash(t.TempDir())
	a := NewApplier()
	changes := []Change{
		{Path: filepath.Join(dir, "index.html"), Orig: "", New: "<html></html>\n"},
		{Path: filepath.Join(dir, "sub", "deep", "x.txt"), Orig: "", New: "hello\n"},
	}
	if err := a.Apply(changes); err != nil {
		t.Fatalf("apply creation: %v", err)
	}
	for _, c := range changes {
		b, err := os.ReadFile(filepath.FromSlash(c.Path))
		if err != nil {
			t.Fatalf("created file %s missing: %v", c.Path, err)
		}
		if string(b) != c.New {
			t.Fatalf("content mismatch in %s", c.Path)
		}
	}
	// A missing file with non-empty Orig is still a stale patch, not a create.
	err := a.Apply([]Change{{Path: filepath.Join(dir, "nope.txt"), Orig: "old", New: "new"}})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("expected stale-patch error, got %v", err)
	}
}

// TestApplyCreationWithFakeFS simulates the applier's Read returning a
// not-found error to drive the "does not exist" creation path, and verifies
// rollback removes a file the series created when a later write fails.
func TestApplyCreationWithFakeFS(t *testing.T) {
	notFound := errors.New("cannot find the file specified")
	fs := map[string]string{}
	ex := map[string]bool{}
	a := NewApplier()
	a.Read = func(p string) (string, error) {
		if !ex[p] {
			return "", notFound
		}
		return fs[p], nil
	}
	a.Write = func(p, c string) error {
		if p == "boom.go" {
			return errors.New("disk full")
		}
		fs[p] = c
		ex[p] = true
		return nil
	}
	removed := []string{}
	a.Remove = func(p string) error {
		removed = append(removed, p)
		ex[p] = false
		return nil
	}

	// Single creation succeeds and is validated as a create (no stale error).
	changes := []Change{{Path: "new.go", Orig: "", New: "package x\n"}}
	if err := a.Apply(changes); err != nil {
		t.Fatalf("apply creation failed: %v", err)
	}
	if !ex["new.go"] || fs["new.go"] != "package x\n" {
		t.Fatalf("new.go not written: %q", fs["new.go"])
	}

	// Two-change series: first creates b.go, second fails -> b.go must be removed.
	if err := a.Apply([]Change{
		{Path: "b.go", Orig: "", New: "b\n"},
		{Path: "boom.go", Orig: "", New: "x\n"},
	}); err == nil {
		t.Fatal("expected the second write to fail")
	}
	if ex["b.go"] {
		t.Fatal("created b.go must be rolled back (removed) on partial failure")
	}
	if len(removed) != 1 || removed[0] != "b.go" {
		t.Fatalf("expected Remove(b.go), got %v", removed)
	}
}
