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
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.clipboard != "hello" {
		t.Fatalf("expected clipboard 'hello', got %q", m.clipboard)
	}

	// Move cursor to end of line
	m = press(m, tea.KeyMsg{Type: tea.KeyEnd})
	if m.cur().buf.HasSelection() {
		t.Fatal("selection should clear on move")
	}

	// Paste (Ctrl+V)
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlV})
	if !strings.HasPrefix(m.cur().buf.Text(), "hello worldhello") {
		t.Fatalf("after paste: %q", m.cur().buf.Text())
	}

	// Cut (Ctrl+X with selection)
	m.cur().buf.SetCursor(0, 0)
	for i := 0; i < 5; i++ {
		m.cur().buf.MoveRightWithSelect()
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlX})
	if m.clipboard != "hello" {
		t.Fatalf("expected clipboard after cut 'hello', got %q", m.clipboard)
	}
	if !strings.HasPrefix(m.cur().buf.Text(), " worldhello") {
		t.Fatalf("after cut: %q", m.cur().buf.Text())
	}
}
