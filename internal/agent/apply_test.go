package agent

import (
	"errors"
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
