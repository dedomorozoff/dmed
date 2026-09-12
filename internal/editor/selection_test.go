package editor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestSelectionCopyPasteCut(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "sel.txt", "hello world\nsecond line\n")

	m := New(f)
	m.width, m.height = 80, 24

	// Move right with select (Shift+Right 5 times to select "hello")
	for i := 0; i < 5; i++ {
		m.cur().buf.MoveRightWithSelect()
	}

	if !m.cur().buf.HasSelection() {
		t.Fatal("expected buffer to have selection")
	}
	if m.cur().buf.SelectedText() != "hello" {
		t.Fatalf("expected 'hello', got %q", m.cur().buf.SelectedText())
	}

	// Copy (Ctrl+C)
	m = press(m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if m.clipboard != "hello" {
		t.Fatalf("expected clipboard 'hello', got %q", m.clipboard)
	}

	// Move cursor to end of line
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnd})
	if m.cur().buf.HasSelection() {
		t.Fatal("selection should clear on move")
	}

	// Paste (Ctrl+V)
	m = press(m, tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	if !strings.HasPrefix(m.cur().buf.Text(), "hello worldhello") {
		t.Fatalf("after paste: %q", m.cur().buf.Text())
	}

	// Cut (Ctrl+X with selection)
	m.cur().buf.SetCursor(0, 0)
	for i := 0; i < 5; i++ {
		m.cur().buf.MoveRightWithSelect()
	}
	m = press(m, tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	if m.clipboard != "hello" {
		t.Fatalf("expected clipboard after cut 'hello', got %q", m.clipboard)
	}
	if !strings.HasPrefix(m.cur().buf.Text(), " worldhello") {
		t.Fatalf("after cut: %q", m.cur().buf.Text())
	}
}

func TestUppercaseSelectionAndBuffer(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "up.txt", "hello world\nsecond line\n")

	// Whole buffer (no selection) via Ctrl+U.
	m := New(f)
	m.width, m.height = 80, 24
	m = press(m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if got := m.cur().buf.Text(); got != "HELLO WORLD\nSECOND LINE\n" {
		t.Fatalf("whole-buffer uppercase got %q", got)
	}

	// Selection only.
	m = New(f)
	m.width, m.height = 80, 24
	for i := 0; i < 5; i++ {
		m.cur().buf.MoveRightWithSelect()
	}
	m = press(m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	want := "HELLO world\nsecond line\n"
	if got := m.cur().buf.Text(); got != want {
		t.Fatalf("selection uppercase got %q want %q", got, want)
	}
	if m.cur().buf.HasSelection() {
		t.Fatal("selection should clear after uppercase")
	}
}
