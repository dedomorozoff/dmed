package editor

import (
	"path/filepath"
	"testing"

	"dmed/internal/session"
)

func TestSessionAutoSaveAndRestore(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "doc1.txt", "one\n")
	f2 := writeTemp(t, dir, "doc2.txt", "two\n")

	m := New(f1, f2)
	m.root = dir

	// Save session
	m.saveSession()

	sessFile := session.DefaultPath(dir)
	loaded, err := session.Load(sessFile)
	if err != nil {
		t.Fatal(err)
	}

	absF1, _ := filepath.Abs(f1)
	absF2, _ := filepath.Abs(f2)

	if len(loaded.Files) != 2 {
		t.Fatalf("expected 2 saved files, got %d", len(loaded.Files))
	}
	if loaded.Files[0] != absF1 || loaded.Files[1] != absF2 {
		t.Fatalf("saved files mismatch: %+v", loaded.Files)
	}
}
