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

func TestSessionCursorSaveAndRestore(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "doc1.txt", "line one\nline two\nline three\n")

	m := New(f1)
	m.root = dir
	m.cur().buf.SetCursor(2, 3)
	m.saveSession()

	loaded, err := session.Load(session.DefaultPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	absF1, _ := filepath.Abs(f1)
	cp, ok := loaded.Cursors[absF1]
	if !ok {
		t.Fatalf("no cursor saved for %s", absF1)
	}
	if cp.Line != 2 || cp.Col != 3 {
		t.Fatalf("saved cursor=%+v want line=2 col=3", cp)
	}

	// A fresh model opening the same file must land back on that position.
	m2 := New(f1)
	m2.root = dir
	m2.restoreCursors(loaded.Cursors)
	if got := m2.cur().buf.CurLine(); got != 2 {
		t.Fatalf("restored line=%d want 2", got)
	}
	if got := m2.cur().buf.Col(); got != 3 {
		t.Fatalf("restored col=%d want 3", got)
	}

	// Stale out-of-range positions are clamped, not crashed on.
	m3 := New(f1)
	m3.root = dir
	m3.restoreCursors(map[string]session.CursorPos{absF1: {Line: 999, Col: 999}})
	if got := m3.cur().buf.CurLine(); got != 2 {
		t.Fatalf("clamped line=%d want 2", got)
	}
}

func TestSessionRestoresOnProjectOpen(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTemp(t, dir, "doc1.txt", "a\nb\nc\n")
	f2 := writeTemp(t, dir, "doc2.txt", "x\ny\nz\n")

	// First run: open two files, move the cursor (active tab is doc2), save.
	m := New(f1, f2)
	m.root = dir
	m.cur().buf.SetCursor(2, 1)
	m.saveSession()

	// Second run: opening the project directory must restore files, cursors,
	// and the active tab.
	m2 := New(dir)
	if len(m2.tabs) != 2 {
		t.Fatalf("expected 2 restored tabs, got %d", len(m2.tabs))
	}
	absF2, _ := filepath.Abs(f2)
	found := false
	for _, tb := range m2.tabs {
		if tb.path == absF2 {
			found = true
			if tb.buf.CurLine() != 2 || tb.buf.Col() != 1 {
				t.Fatalf("restored doc2 cursor=%d,%d want 2,1", tb.buf.CurLine(), tb.buf.Col())
			}
		}
	}
	if !found {
		t.Fatal("doc2 must be reopened from the session")
	}
	if got := m2.activeTabIndex(); got != 1 {
		t.Fatalf("active tab=%d want 1", got)
	}
	_ = f1
}
