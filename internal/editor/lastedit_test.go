package editor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/buffer"
)

// "Go to last change" is a caret move with one jump-list push: the position comes
// from the buffer, both refusals leave the caret alone, and nothing in the path
// edits, dirties or spends an undo step.

var lastEditChord = tea.KeyPressMsg{Code: tea.KeyBackspace, Mod: tea.ModCtrl | tea.ModShift}

func lastEditModel(t *testing.T, content string) Model {
	t.Helper()
	m := New(writeTemp(t, t.TempDir(), "lastedit.txt", content))
	m.width, m.height = 80, 24
	return m
}

// typeInto types through the key pipeline and dismisses the completion popup the
// keystroke leaves open: while that popup owns the keyboard the next chord answers
// to it instead of the editor.
func typeInto(t *testing.T, m Model, s string) Model {
	t.Helper()
	m = typeStr(m, s)
	if m.complOpen {
		m = press(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	}
	if m.complOpen {
		t.Fatal("Escape must dismiss the completion popup")
	}
	return m
}

func TestLastEditGoesToWhereTheEditWas(t *testing.T) {
	m := lastEditModel(t, "aaa\nbbb\nccc\n")
	place(m, 1, 2)
	m = typeInto(t, m, "X")
	if ln, c := caret(m); ln != 1 || c != 3 {
		t.Fatalf("setup: the caret must ride after the typed rune, got (%d,%d)", ln, c)
	}

	m = gotoLine(m, "3") // walk away from the change
	m = press(m, lastEditChord)

	if ln, c := caret(m); ln != 1 || c != 2 {
		t.Fatalf("caret = (%d,%d), want the edit at (1,2)", ln, c)
	}
	if m.msg != "last change: line 2" {
		t.Fatalf("msg = %q, want the 1-based line the caret reached", m.msg)
	}
	mustText(t, m.cur().buf.Text(), "aaa\nbbXb\nccc\n")
}

// The command moves the caret somewhere the user cannot walk back to by hand, so
// it is a jump producer like the four that shipped first.
func TestLastEditPushesTheJumpList(t *testing.T) {
	m := lastEditModel(t, "aaa\nbbb\nccc\n")
	place(m, 0, 1)
	m = typeInto(t, m, "X")
	m = gotoLine(m, "3") // the caret now stands on (2,0)

	m = press(m, lastEditChord)
	if ln, c := caret(m); ln != 0 || c != 1 {
		t.Fatalf("the chord landed at (%d,%d), want the edit at (0,1)", ln, c)
	}
	m = press(m, backChord)
	if ln, c := caret(m); ln != 2 || c != 0 {
		t.Fatalf("back = (%d,%d), want the position the chord left (2,0)", ln, c)
	}
}

func TestLastEditTwiceRefusesAndPushesNothing(t *testing.T) {
	m := lastEditModel(t, "aaa\nbbb\nccc\n")
	place(m, 1, 2)
	m = typeInto(t, m, "X")
	m = gotoLine(m, "3")
	m = press(m, lastEditChord)

	depth := len(m.curPane().backs)
	m = press(m, lastEditChord)
	if m.msg != "already at the last change" {
		t.Fatalf("msg = %q", m.msg)
	}
	if ln, c := caret(m); ln != 1 || c != 2 {
		t.Fatalf("a refusal must leave the caret alone, got (%d,%d)", ln, c)
	}
	if len(m.curPane().backs) != depth {
		t.Fatalf("a refusal must spend no jump slot, backs = %d, want %d", len(m.curPane().backs), depth)
	}
}

func TestLastEditOnUneditedFileRefuses(t *testing.T) {
	m := lastEditModel(t, "aaa\nbbb\nccc\n")
	place(m, 1, 1)

	m = press(m, lastEditChord)
	if m.msg != "nothing has been edited in this file" {
		t.Fatalf("msg = %q", m.msg)
	}
	if ln, c := caret(m); ln != 1 || c != 1 {
		t.Fatalf("caret = (%d,%d), want the untouched (1,1)", ln, c)
	}
	if len(m.curPane().backs) != 0 {
		t.Fatalf("a refusal must push nothing, backs = %v", m.curPane().backs)
	}
}

// Undo removes the change but not the answer: after a Ctrl+Z the place the user was
// working is exactly where they want the caret back.
func TestLastEditSurvivesUndo(t *testing.T) {
	m := lastEditModel(t, "aaa\nbbb\nccc\n")
	place(m, 1, 2)
	m = typeInto(t, m, "X")
	m = press(m, undoChord)
	mustText(t, m.cur().buf.Text(), "aaa\nbbb\nccc\n")

	m = gotoLine(m, "3") // undo restored the caret, so walk away again
	m = press(m, lastEditChord)
	if ln, c := caret(m); ln != 1 || c != 2 {
		t.Fatalf("caret = (%d,%d), want the undone edit at (1,2)", ln, c)
	}
}

func TestLastEditIsNotAnEdit(t *testing.T) {
	m := lastEditModel(t, "aaa\nbbb\nccc\n")
	place(m, 1, 2)
	m = typeInto(t, m, "X")
	m = gotoLine(m, "3")

	before, dirty := m.cur().buf.Text(), m.cur().buf.Dirty()
	m = press(m, lastEditChord)
	if m.cur().buf.Text() != before {
		t.Fatalf("the chord must not touch the text, got %q", m.cur().buf.Text())
	}
	if m.cur().buf.Dirty() != dirty {
		t.Fatal("the chord must not change the dirty flag")
	}
	// The change is still the step Ctrl+Z removes: the command spent no undo slot.
	m = press(m, undoChord)
	mustText(t, m.cur().buf.Text(), "aaa\nbbb\nccc\n")
}

func TestLastEditClearsSecondaryCursors(t *testing.T) {
	m := lastEditModel(t, "aaa\naaa\naaa\n")
	place(m, 0, 0)
	m = typeInto(t, m, "X") // the edit is at (0,0), the caret now at (0,1)
	m.cur().buf.AddCursor(2, 1, 1, 1)
	if !m.cur().buf.HasMultipleCursors() {
		t.Fatal("setup: the secondary cursor must be armed")
	}

	m = press(m, lastEditChord)
	if m.cur().buf.HasMultipleCursors() {
		t.Fatal("the chord must land with one caret")
	}
	if ln, c := caret(m); ln != 0 || c != 0 {
		t.Fatalf("caret = (%d,%d), want the edit at (0,0)", ln, c)
	}
}

// A fold that hides the landing line must not swallow the answer: clampScroll
// unfolds the block the caret re-enters.
func TestLastEditUnfoldsTheLandingLine(t *testing.T) {
	m := lastEditModel(t, foldSrc)
	place(m, 2, 1)
	m = typeInto(t, m, "X") // inside the nested block, hidden by the outer fold
	m.tabs[0].folds = map[int]bool{0: true}
	m = gotoLine(m, "7")

	m = press(m, lastEditChord)
	if ln, c := caret(m); ln != 2 || c != 1 {
		t.Fatalf("caret = (%d,%d), want the edit at (2,1)", ln, c)
	}
	if len(m.tabs[0].folds) != 0 {
		t.Fatalf("a fold hiding the landing line must be opened again, folds = %v", m.tabs[0].folds)
	}
	if _, _, hid := m.tabs[0].foldHidden(2, m.cfg.Editor.TabWidth); hid {
		t.Fatal("the landing line must be visible")
	}
}

// An LSP-style edit away from the caret can delete the tail of the file the
// tracker is still pointing at. SetCursor clamps, so the chord lands on what is
// left instead of stranding the caret past the end.
func TestLastEditClampsAfterTheFileShrank(t *testing.T) {
	m := lastEditModel(t, "one\ntwo\nthree\nfour\nfive\n")
	place(m, 4, 2)
	m = typeInto(t, m, "X") // the tracker keeps (4,2), the caret rides to (4,3)
	if !m.cur().buf.ReplaceSpan(1, 0, 4, len("fiXve"), "end") {
		t.Fatal("ReplaceSpan = false")
	}
	if m.cur().buf.LineCount() != 2 {
		t.Fatalf("setup: the file must be down to two lines, got %q", m.cur().buf.Text())
	}

	m = press(m, lastEditChord)
	ln, c := caret(m)
	if ln < 0 || ln >= m.cur().buf.LineCount() || c > m.cur().buf.LineLen(ln) {
		t.Fatalf("the caret escaped the document at (%d,%d), text %q", ln, c, m.cur().buf.Text())
	}
	if m.msg != "last change: line 2" {
		t.Fatalf("msg = %q, want the clamped line", m.msg)
	}
}

func TestLastEditPaletteCommand(t *testing.T) {
	m := lastEditModel(t, "aaa\nbbb\nccc\n")
	place(m, 1, 2)
	m = typeInto(t, m, "X")
	m = gotoLine(m, "3")

	m = press(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if !m.paletteOpen {
		t.Fatal("the palette must open")
	}
	m = typeStr(m, "last change")
	sel := -1
	for i, h := range m.filterPalette() {
		if h.id == "last_edit" {
			sel = i
		}
	}
	if sel < 0 {
		t.Fatalf("filtering must find the command, got %v", paletteIDs(m.filterPalette()))
	}
	m.paletteSel = sel
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.paletteOpen {
		t.Fatal("executing a command closes the palette")
	}

	viaChord := lastEditModel(t, "aaa\nbbb\nccc\n")
	place(viaChord, 1, 2)
	viaChord = typeInto(t, viaChord, "X")
	viaChord = gotoLine(viaChord, "3")
	viaChord = press(viaChord, lastEditChord)
	pLn, pCol := caret(m)
	cLn, cCol := caret(viaChord)
	if pLn != cLn || pCol != cCol {
		t.Fatalf("palette and chord disagree: palette (%d,%d), chord (%d,%d)", pLn, pCol, cLn, cCol)
	}
}

func TestHelpListsLastEditRow(t *testing.T) {
	const wantKey = "Ctrl+Shift+Backspace"
	found := false
	for _, e := range helpEntries {
		if e.keys == wantKey {
			found = true
		}
	}
	if !found {
		t.Fatalf("helpEntries is missing the %q row", wantKey)
	}

	m := lastEditModel(t, "aaa\n")
	m.width, m.height = 120, 24
	m = press(m, tea.KeyPressMsg{Code: tea.KeyF1})
	for i, e := range helpEntries {
		if e.keys == wantKey {
			m.helpScroll = i
		}
	}
	out := m.View().Content
	for _, want := range []string{wantKey, m.t("help.last_edit")} {
		if !strings.Contains(out, want) {
			t.Fatalf("the help must show %q", want)
		}
	}
}

// The tracker belongs to the buffer, so a second file answers for itself: the
// chord in an untouched tab must not report the change made in another one.
func TestLastEditIsPerFile(t *testing.T) {
	dir := t.TempDir()
	first := writeTemp(t, dir, "first.txt", "aaa\nbbb\n")
	second := writeTemp(t, dir, "second.txt", "ccc\nddd\n")
	m := New(first, second)
	m.width, m.height = 80, 24

	place(m, 1, 1) // the second tab is on screen
	m = typeInto(t, m, "X")
	m = gotoLine(m, "1")

	m.setActiveTab(0)
	m = press(m, lastEditChord)
	if m.msg != "nothing has been edited in this file" {
		t.Fatalf("the other file must answer for its own changes, msg = %q", m.msg)
	}

	m.setActiveTab(1)
	m = press(m, lastEditChord)
	if ln, c := caret(m); ln != 1 || c != 1 {
		t.Fatalf("caret = (%d,%d), want the edit in the second file at (1,1)", ln, c)
	}
}

// The command is a caret move on the current tab and nothing more: with no tab at
// all it must not panic, and the terminal pane keeps its own keys.
func TestLastEditOnAnEmptyBufferDoesNotPanic(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.tabs = []tab{{buf: buffer.New()}}
	m.curPane().tabIdx = 0
	m = press(m, lastEditChord)
	if m.msg != "nothing has been edited in this file" {
		t.Fatalf("msg = %q", m.msg)
	}
}
