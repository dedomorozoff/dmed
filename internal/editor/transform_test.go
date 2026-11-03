package editor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The transformation commands are deliberately thin: they pick the target, call
// the buffer method and report the count. These tests cover what only exists at
// this layer — the chords, trim's whole-file fallback, the status-bar messages,
// the palette routes and the help rows.

var (
	lowercaseChord = tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl | tea.ModShift}
	sortChord      = tea.KeyPressMsg{Code: 's', Mod: tea.ModAlt | tea.ModShift}
	trimChord      = tea.KeyPressMsg{Code: 't', Mod: tea.ModAlt | tea.ModShift}
	alignChord     = tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl | tea.ModAlt}
	numberChord    = tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl | tea.ModAlt}
	upperChord     = tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
	undoChord      = tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl}
)

func transformModel(t *testing.T, content string) Model {
	t.Helper()
	m := New(writeTemp(t, t.TempDir(), "transform.txt", content))
	m.width, m.height = 80, 24
	return m
}

// selectBlock selects whole lines [from..to] the way the shift-motions do: the
// anchor stays on the first line, the caret rides the end of the last one.
func selectBlock(m Model, from, to int) {
	m.cur().buf.SetCursor(from, 0)
	for i := from; i < to; i++ {
		m.cur().buf.MoveDownWithSelect()
	}
	m.cur().buf.LineEndWithSelect()
}

// TestUppercaseKeepsUndoHistory is the regression the transformation work was
// built on: the whole-buffer fold used to rebuild the buffer with buffer.Load,
// which threw the undo history away, so Ctrl+Z after it restored nothing.
func TestUppercaseKeepsUndoHistory(t *testing.T) {
	m := transformModel(t, "hello\nworld\n")
	m = press(m, upperChord)
	mustText(t, m.cur().buf.Text(), "HELLO\nWORLD\n")
	if m.msg != "uppercased" {
		t.Fatalf("msg = %q", m.msg)
	}
	m = press(m, undoChord)
	mustText(t, m.cur().buf.Text(), "hello\nworld\n")
	if m.cur().buf.Undo() {
		t.Fatal("a whole-buffer fold is one undo step, not several")
	}
}

func TestLowercaseChordFoldsSelectionOrBuffer(t *testing.T) {
	m := transformModel(t, "Hello World\nKeep Me\n")
	selectBlock(m, 0, 0)
	m = press(m, lowercaseChord)
	mustText(t, m.cur().buf.Text(), "hello world\nKeep Me\n")
	if m.msg != "lowercased" {
		t.Fatalf("msg = %q", m.msg)
	}

	m = transformModel(t, "Hello World\nKeep Me\n")
	m = press(m, lowercaseChord)
	mustText(t, m.cur().buf.Text(), "hello world\nkeep me\n")
}

// TestTransformSelectionSurvives: the block keeps being the block after the
// edit, so the same selection can be transformed again — and the caret column is
// clamped to the line that ended up under it.
func TestTransformSelectionSurvives(t *testing.T) {
	cases := []struct {
		name    string
		content string
		chord   tea.KeyPressMsg
		want    string
		msg     string
	}{
		{"sort", "zz\nyy\nxx\n", sortChord, "xx\nyy\nzz\n", "sorted 3 lines"},
		{"trim", "a  \nb\t\nc  \n", trimChord, "a\nb\nc\n", "trimmed 3 lines"},
		{"align", "alpha\n  beta\n    gamma\n", alignChord, "    alpha\n    beta\n    gamma\n", "aligned 2 lines"},
		{"number", "alpha\nbeta\ngamma\n", numberChord, "1. alpha\n2. beta\n3. gamma\n", "numbered 3 lines"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := transformModel(t, tc.content)
			selectBlock(m, 0, 2)
			m = press(m, tc.chord)
			mustText(t, m.cur().buf.Text(), tc.want)
			if m.msg != tc.msg {
				t.Fatalf("msg = %q, want %q", m.msg, tc.msg)
			}
			if from, to := m.cur().buf.SelectionLines(); from != 0 || to != 2 {
				t.Fatalf("selection after %s = %d..%d, want 0..2", tc.name, from, to)
			}
			if c := m.cur().buf.Col(); c > m.cur().buf.LineLen(m.cur().buf.CurLine()) {
				t.Fatalf("caret column %d is past the end of line %d", c, m.cur().buf.CurLine())
			}
			m = press(m, undoChord)
			mustText(t, m.cur().buf.Text(), tc.content)
		})
	}
}

func TestTransformDropsSecondaryCursors(t *testing.T) {
	m := transformModel(t, "ab\nab\nab\n")
	m.cur().buf.SetCursor(0, 0)
	m.cur().buf.AddNextOccurrence() // arms the word selection
	m.cur().buf.AddNextOccurrence() // adds the next occurrence
	mustBool(t, "secondary cursor", m.cur().buf.HasMultipleCursors(), true)

	m = press(m, numberChord)
	if m.cur().buf.HasMultipleCursors() {
		t.Fatal("a block transform permutes whole lines, so it drops the extra cursors")
	}
	mustText(t, m.cur().buf.Text(), "1. ab\nab\nab\n")
}

func TestTrimTrailingWholeBuffer(t *testing.T) {
	// No selection: the whole file is cleaned, which is the pre-commit case.
	m := transformModel(t, "one  \ntwo\t\nthree\n")
	m = press(m, trimChord)
	mustText(t, m.cur().buf.Text(), "one\ntwo\nthree\n")
	if m.msg != "trimmed 2 lines" {
		t.Fatalf("msg = %q", m.msg)
	}

	// With a selection only the touched lines are.
	m = transformModel(t, "one  \ntwo\t\nthree  \n")
	selectBlock(m, 0, 1)
	m = press(m, trimChord)
	mustText(t, m.cur().buf.Text(), "one\ntwo\nthree  \n")

	// An already clean block says so instead of reporting zero lines.
	m = transformModel(t, "one\ntwo\nthree\n")
	m = press(m, trimChord)
	if m.msg != "nothing to change" {
		t.Fatalf("msg = %q", m.msg)
	}
	mustText(t, m.cur().buf.Text(), "one\ntwo\nthree\n")
	if m.cur().buf.Undo() {
		t.Fatal("a trim that changed nothing must not enter the undo history")
	}
}

// TestAlignIndentPreservesTabs: the target column is the deepest line the block
// already has, measured in expanded columns, so the tab-indented line — the
// deepest one — is left byte for byte alone and only the space lines are padded.
func TestAlignIndentPreservesTabs(t *testing.T) {
	m := transformModel(t, "\t\talpha\n  beta\ngamma\n")
	tabW := m.cfg.Editor.TabWidth
	deep := strings.Repeat(" ", 2*tabW)
	selectBlock(m, 0, 2)
	m = press(m, alignChord)

	want := "\t\talpha\n" + deep + "beta\n" + deep + "gamma\n"
	mustText(t, m.cur().buf.Text(), want)
	if m.msg != "aligned 2 lines" {
		t.Fatalf("msg = %q", m.msg)
	}
	// Indentation is never converted: the tabs are still tabs.
	if !strings.HasPrefix(m.cur().buf.Text(), "\t\talpha\n") {
		t.Fatalf("the tab indent was rewritten: %q", m.cur().buf.Text())
	}
}

func TestNumberLinesIsIdempotent(t *testing.T) {
	m := transformModel(t, "alpha\n\nbeta\n1) gamma\n")
	selectBlock(m, 0, 3)
	m = press(m, numberChord)
	want := "1. alpha\n\n2. beta\n3. gamma\n"
	mustText(t, m.cur().buf.Text(), want)

	// The blank line is skipped without consuming a number and the existing
	// marker is replaced, so a second run leaves the text exactly as it is.
	m = press(m, numberChord)
	mustText(t, m.cur().buf.Text(), want)
	m = press(m, undoChord)
	mustText(t, m.cur().buf.Text(), "alpha\n\nbeta\n1) gamma\n")
	if m.cur().buf.Undo() {
		t.Fatal("the second run changed nothing, so it added no undo step")
	}
}

func TestTransformPaletteCommands(t *testing.T) {
	m := transformModel(t, "zz\nyy\nxx\n")
	m = press(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if !m.paletteOpen {
		t.Fatal("the palette must open")
	}
	seen := map[string]bool{}
	for _, hit := range m.filterPalette() {
		seen[hit.id] = true
	}
	for _, id := range []string{"lowercase", "sort_lines", "trim_lines", "align_indent", "number_lines"} {
		if !seen[id] {
			t.Fatalf("the palette must offer %q, got %v", id, paletteIDs(m.filterPalette()))
		}
	}

	// The filter finds them by name, and the palette route mutates the buffer
	// exactly like the chord does.
	m = typeStr(m, "sort")
	hits := m.filterPalette()
	idx := -1
	for i, hit := range hits {
		if hit.id == "sort_lines" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("filtering on \"sort\" must find the sort command, got %v", paletteIDs(hits))
	}
	m.paletteSel = idx
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.paletteOpen {
		t.Fatal("executing a command closes the palette")
	}
	viaChord := press(transformModel(t, "zz\nyy\nxx\n"), sortChord)
	mustText(t, m.cur().buf.Text(), viaChord.cur().buf.Text())
}

// TestHelpListsTransformRows keeps the strings and the table in the same place:
// every transformation row is bound in the keys, listed in the help and must
// actually render.
func TestHelpListsTransformRows(t *testing.T) {
	wantChords := []string{"ctrl+shift+u", "alt+shift+s", "alt+shift+t", "ctrl+alt+j", "ctrl+alt+n"}
	var missing []string
	m := New()
	for _, c := range wantChords {
		if helpIndexOf(m.helpRows(), c) < 0 {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("the help is missing rows for %v", missing)
	}

	m.width, m.height = 120, 24
	m = press(m, tea.KeyPressMsg{Code: tea.KeyF1})
	m = press(m, tea.KeyPressMsg{Text: "G"}) // jump to the end of the list
	out := m.View().Content
	for _, want := range []string{
		"Ctrl+Shift+U", "Alt+Shift+S", "Alt+Shift+T", "Ctrl+Alt+J", "Ctrl+Alt+N",
		m.t("help.lowercase"), m.t("help.sort_lines"), m.t("help.trim_lines"),
		m.t("help.align_indent"), m.t("help.number_lines"),
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the scrolled help must show %q", want)
		}
	}
}
