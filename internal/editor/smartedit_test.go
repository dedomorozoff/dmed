package editor

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestTabIndentsBlockAndShiftTabUnindents(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "ind.txt", "func a() {\nb\n}\n")
	m := New(f)
	m.width, m.height = 80, 24

	// Select lines 1..2 and indent them as a block.
	b := m.cur().buf
	b.SetCursor(1, 0)
	b.MoveDownWithSelect()
	m = press(m, tea.KeyPressMsg{Code: tea.KeyTab})
	if got := m.cur().buf.Text(); got != "func a() {\n\tb\n\t}\n" {
		t.Fatalf("after Tab: %q", got)
	}
	// The block stays selected: Shift+Tab hits the same lines.
	if !m.cur().buf.HasSelection() {
		t.Fatal("block Tab keeps the selection")
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if got := m.cur().buf.Text(); got != "func a() {\nb\n}\n" {
		t.Fatalf("after Shift+Tab: %q", got)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if got := m.cur().buf.Text(); got != "func a() {\nb\n}\n" {
		t.Fatalf("Shift+Tab must not eat content: %q", got)
	}
}

func TestTabNoSelectionStillInsertsTab(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "t.txt", "x\n")
	m := New(f)
	m = press(m, tea.KeyPressMsg{Code: tea.KeyTab})
	if got := m.cur().buf.Text(); got != "\tx\n" {
		t.Fatalf("plain Tab: %q", got)
	}
}

func TestEnterContinuationIndent(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "ent.txt", "\tfoo\n")
	m := New(f)
	m.width, m.height = 80, 24

	m.cur().buf.SetCursor(0, 4)
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := m.cur().buf.Text(); got != "\tfoo\n\t\n" {
		t.Fatalf("plain continuation: %q", got)
	}
	if c := m.cur().buf.Col(); c != 1 {
		t.Fatalf("caret on the indent: col=%d", c)
	}

	// An open bracket before the caret indents one level deeper (relative to
	// the line's own leading whitespace).
	f = writeTemp(t, dir, "ent2.txt", "if true {\n")
	m = New(f)
	m.cur().buf.SetCursor(0, 9) // end of "if true {"
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := m.cur().buf.Text(); got != "if true {\n\t\n" {
		t.Fatalf("bracket continuation: %q", got)
	}
}

func TestAutoClosePairAndTypeOver(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "ac.txt", "")
	m := New(f)
	m.width, m.height = 80, 24

	m = typeStr(m, "foo(")
	if got := string(m.cur().buf.LineAt(0)); got != "foo()" {
		t.Fatalf("auto-close pair: %q", got)
	}
	// The caret sits between the parentheses.
	if c := m.cur().buf.Col(); c != 4 {
		t.Fatalf("caret between the pair: col=%d", c)
	}

	// Typing the closer steps over it instead of duplicating.
	m = typeStr(m, ")")
	if got := string(m.cur().buf.LineAt(0)); got != "foo()" {
		t.Fatalf("type-over closer: %q", got)
	}
	if c := m.cur().buf.Col(); c != 5 {
		t.Fatalf("caret after step-over: col=%d", c)
	}

	// A quote in blank context wraps; the closing quote is typed over.
	m = typeStr(m, " \"x\"")
	if got := string(m.cur().buf.LineAt(0)); got != `foo() "x"` {
		t.Fatalf("quote wrap: %q", got)
	}
	if c := m.cur().buf.Col(); c != 9 {
		t.Fatalf("caret after quote wrap: col=%d", c)
	}
}

func TestQuoteInsideWordIsNotWrapped(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "q.txt", "ab\n")
	m := New(f)
	m.cur().buf.SetCursor(0, 1) // between a and b
	m = typeStr(m, "\"")
	if got := string(m.cur().buf.LineAt(0)); got != "a\"b" {
		t.Fatalf("quote inside a word must not wrap: %q", got)
	}
}

func TestPairBackspace(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "pb.txt", "")
	m := New(f)
	m.width, m.height = 80, 24

	m = typeStr(m, "{")
	if got := string(m.cur().buf.LineAt(0)); got != "{}" {
		t.Fatalf("pair: %q", got)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if got := string(m.cur().buf.LineAt(0)); got != "" {
		t.Fatalf("pair backspace: %q", got)
	}
	// The whole pair goes in one undo step.
	m.cur().buf.Undo()
	if got := string(m.cur().buf.LineAt(0)); got != "{}" {
		t.Fatalf("after undo: %q", got)
	}
	m.cur().buf.Redo()
	if got := string(m.cur().buf.LineAt(0)); got != "" {
		t.Fatalf("after redo: %q", got)
	}

	// Without a pair underneath, Backspace stays plain.
	m = typeStr(m, "a) ")
	m = press(m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if got := string(m.cur().buf.LineAt(0)); got != "a)" {
		t.Fatalf("plain backspace: %q", got)
	}
}

func TestEditorTypingOverSelectionKeepsFirstChar(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "sel.txt", "hello\n")
	m := New(f)
	m.width, m.height = 80, 24

	b := m.cur().buf
	b.SetCursor(0, 0)
	for i := 0; i < 5; i++ {
		b.MoveRightWithSelect()
	}
	m = typeStr(m, "bye")
	if got := m.cur().buf.Text(); got != "bye\n" {
		t.Fatalf("typing over selection: %q", got)
	}
}
