package editor

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/buffer"
)

func TestAltDMultiCursorEdit(t *testing.T) {
	m := New()
	m.tabs[0].buf = buffer.Load("cat dog cat bird cat")
	m = press(m, tea.KeyPressMsg{Mod: tea.ModAlt, Code: 'd'})
	if !m.cur().buf.HasSelection() {
		t.Fatal("expected first Alt+D to select the word under cursor")
	}
	// Add cursors on the next two occurrences.
	m = press(m, tea.KeyPressMsg{Mod: tea.ModAlt, Code: 'd'})
	m = press(m, tea.KeyPressMsg{Mod: tea.ModAlt, Code: 'd'})
	if !m.cur().buf.HasMultipleCursors() {
		t.Fatal("expected multiple cursors after Alt+D")
	}
	// Typing replaces the selected words at every cursor.
	m = typeStr(m, "X")
	got := m.cur().buf.Text()
	want := "X dog X bird X\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestEscClearsMultiCursor(t *testing.T) {
	m := New()
	m.tabs[0].buf = buffer.Load("cat cat cat")
	m = press(m, tea.KeyPressMsg{Mod: tea.ModAlt, Code: 'd'})
	m = press(m, tea.KeyPressMsg{Mod: tea.ModAlt, Code: 'd'})
	if !m.cur().buf.HasMultipleCursors() {
		t.Fatal("expected multiple cursors")
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.cur().buf.HasMultipleCursors() {
		t.Fatal("expected Esc to clear cursors")
	}
}

func TestAltClickAddsCursor(t *testing.T) {
	m := New()
	m.tabs[0].buf = buffer.Load("hello world")
	// Click normally to place the main cursor at (0,2).
	next, _ := m.Update(tea.MouseClickMsg{X: 8, Y: 1})
	m = next.(Model)
	if m.cur().buf.CurLine() != 0 || m.cur().buf.Col() != 2 {
		t.Fatalf("main cursor not placed: line=%d col=%d", m.cur().buf.CurLine(), m.cur().buf.Col())
	}
	// Alt+Click adds a secondary cursor at (0,6) without moving the main one.
	next, _ = m.Update(tea.MouseClickMsg{X: 11, Y: 1, Mod: tea.ModAlt})
	m = next.(Model)
	if !m.cur().buf.HasMultipleCursors() {
		t.Fatal("expected Alt+Click to add a cursor")
	}
	if m.cur().buf.Col() != 2 {
		t.Fatalf("Alt+Click must not move the main cursor, col=%d", m.cur().buf.Col())
	}
	if got := m.cur().buf.Text(); got != "hello world\n" {
		t.Fatalf("buffer mutated: %q", got)
	}
}
