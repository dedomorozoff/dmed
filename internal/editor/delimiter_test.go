package editor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The bracket commands are thin too: the scanning lives in internal/buffer. What
// only exists here is the chord, the refusal message, the caret being left alone
// on a refusal and the palette/help routes.

var (
	jumpChord       = tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl}
	jumpLegacyChord = tea.KeyPressMsg{Text: "\x1d"} // the Ctrl+] control byte
	selectChord     = tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl | tea.ModShift}
	altDChord       = tea.KeyPressMsg{Code: 'd', Mod: tea.ModAlt}
)

func delimModel(t *testing.T, content string) Model {
	t.Helper()
	m := New(writeTemp(t, t.TempDir(), "delim.txt", content))
	m.width, m.height = 80, 24
	return m
}

// place parks the cursor and drops any selection, the way a click would.
func place(m Model, line, col int) {
	m.cur().buf.SetCursor(line, col)
	m.cur().buf.Deselect()
}

func TestBracketJumpChordMovesTheCaret(t *testing.T) {
	m := delimModel(t, "foo(bar)\n")

	// From on the opener and from inside the pair: both land on the closer.
	place(m, 0, 3)
	m = press(m, jumpChord)
	if ln, c := m.cur().buf.CurLine(), m.cur().buf.Col(); ln != 0 || c != 7 {
		t.Fatalf("from the opener: caret = (%d,%d), want (0,7)", ln, c)
	}
	place(m, 0, 5)
	m = press(m, jumpChord)
	if ln, c := m.cur().buf.CurLine(), m.cur().buf.Col(); ln != 0 || c != 7 {
		t.Fatalf("from inside: caret = (%d,%d), want (0,7)", ln, c)
	}
	if m.msg != "" {
		t.Fatalf("a successful jump clears the status line, got %q", m.msg)
	}

	// From the closer back to the opener.
	m = press(m, jumpChord)
	if ln, c := m.cur().buf.CurLine(), m.cur().buf.Col(); ln != 0 || c != 3 {
		t.Fatalf("from the closer: caret = (%d,%d), want (0,3)", ln, c)
	}
}

// TestBracketJumpWalksThePair is why there is no backward chord: Ctrl+[ *is*
// Escape, so one chord has to do both directions.
func TestBracketJumpWalksThePair(t *testing.T) {
	m := delimModel(t, "f(g(h))\n")

	place(m, 0, 4) // inside the innermost pair, on 'h'
	m = press(m, jumpChord)
	if _, c := m.cur().buf.CurLine(), m.cur().buf.Col(); c != 5 {
		t.Fatalf("first press: col = %d, want the closer at 5", c)
	}
	m = press(m, jumpChord)
	if _, c := m.cur().buf.CurLine(), m.cur().buf.Col(); c != 3 {
		t.Fatalf("second press: col = %d, want back at the opener 3", c)
	}
}

// TestBracketJumpLegacyByte: a terminal that cannot tell Ctrl+] from the shifted
// key sends the control byte, which keyOf re-encodes as ctrl+}. The alias must
// reach the same command instead of typing anything.
func TestBracketJumpLegacyByte(t *testing.T) {
	m := delimModel(t, "foo(bar)\n")
	place(m, 0, 3)
	m = press(m, jumpLegacyChord)
	if _, c := m.cur().buf.CurLine(), m.cur().buf.Col(); c != 7 {
		t.Fatalf("legacy byte: col = %d, want 7", c)
	}
	mustText(t, m.cur().buf.Text(), "foo(bar)\n")
}

// TestBracketJumpAcrossLines: the partner of a brace opened on another line is
// on that other line, and the scroll window follows the cursor.
func TestBracketJumpAcrossLines(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("func main() {\n")
	for i := 0; i < 40; i++ {
		sb.WriteString("\tprintln(\"x\")\n")
	}
	sb.WriteString("}\n")
	m := delimModel(t, sb.String())

	place(m, 0, 12)
	m = press(m, jumpChord)
	if ln, c := m.cur().buf.CurLine(), m.cur().buf.Col(); ln != 41 || c != 0 {
		t.Fatalf("caret = (%d,%d), want the closing brace at (41,0)", ln, c)
	}
	if m.panes[m.activePane].offsetY > 41 {
		t.Fatalf("scrolled to %d, the cursor line must be in view", m.panes[m.activePane].offsetY)
	}
}

func TestBracketJumpRefusalLeavesTheCaretAlone(t *testing.T) {
	m := delimModel(t, "plain text here\nand (unclosed\n")

	place(m, 0, 6)
	before := m.cur().buf.Text()
	m = press(m, jumpChord)
	if m.msg != "no matching bracket around the cursor" {
		t.Fatalf("msg = %q", m.msg)
	}
	if ln, c := m.cur().buf.CurLine(), m.cur().buf.Col(); ln != 0 || c != 6 {
		t.Fatalf("a refusal must not move the caret: (%d,%d)", ln, c)
	}
	mustText(t, m.cur().buf.Text(), before)

	// An unclosed bracket is a refusal too, never a jump to a guessed partner.
	place(m, 1, 4)
	m = press(m, jumpChord)
	if m.msg != "no matching bracket around the cursor" {
		t.Fatalf("msg = %q", m.msg)
	}
	if _, c := m.cur().buf.CurLine(), m.cur().buf.Col(); c != 4 {
		t.Fatalf("caret moved to col %d on an unclosed bracket", c)
	}
}

func TestSelectInsideBracketsSelectsTheContent(t *testing.T) {
	m := delimModel(t, "foo(bar)\n")

	place(m, 0, 5)
	m = press(m, selectChord)
	b := m.cur().buf
	if !b.HasSelection() {
		t.Fatal("expected the pair's content to be selected")
	}
	if got := b.SelectedText(); got != "bar" {
		t.Fatalf("SelectedText = %q, want \"bar\"", got)
	}
	// The delimiters are excluded and the caret ends up at the end of the range,
	// so a shift-arrow or a copy continues from there.
	if sl, sc, el, ec := b.SelectionRange(); sl != 0 || sc != 4 || el != 0 || ec != 7 {
		t.Fatalf("range = (%d,%d)..(%d,%d), want (0,4)..(0,7)", sl, sc, el, ec)
	}
	if ln, c := b.CurLine(), b.Col(); ln != 0 || c != 7 {
		t.Fatalf("caret = (%d,%d), want (0,7)", ln, c)
	}
	mustText(t, b.Text(), "foo(bar)\n")

	// Repeating it on the same spot gives the same range: the command replaces
	// the selection instead of growing it.
	place(m, 0, 5)
	m = press(m, selectChord)
	if got := m.cur().buf.SelectedText(); got != "bar" {
		t.Fatalf("second run SelectedText = %q, want \"bar\"", got)
	}
}

func TestSelectInsideBracketsFromOnADelimiter(t *testing.T) {
	m := delimModel(t, `s := "hi there"`+"\n")

	// On the opening quote: the string body, quotes excluded.
	place(m, 0, 5)
	m = press(m, selectChord)
	if got := m.cur().buf.SelectedText(); got != "hi there" {
		t.Fatalf("SelectedText = %q, want the string body", got)
	}
}

func TestSelectInsideEmptyBrackets(t *testing.T) {
	m := delimModel(t, "foo()\n")

	place(m, 0, 3)
	m = press(m, selectChord)
	if m.msg != "nothing between the brackets" {
		t.Fatalf("msg = %q", m.msg)
	}
	if m.cur().buf.HasSelection() {
		t.Fatal("an empty pair has nothing to select")
	}
	if ln, c := m.cur().buf.CurLine(), m.cur().buf.Col(); ln != 0 || c != 4 {
		t.Fatalf("caret = (%d,%d), want between the delimiters at (0,4)", ln, c)
	}
}

func TestSelectInsideRefusalKeepsEverything(t *testing.T) {
	m := delimModel(t, "plain text\n")

	place(m, 0, 3)
	m = press(m, selectChord)
	if m.msg != "no matching bracket around the cursor" {
		t.Fatalf("msg = %q", m.msg)
	}
	if m.cur().buf.HasSelection() {
		t.Fatal("a refusal must not select anything")
	}
	if ln, c := m.cur().buf.CurLine(), m.cur().buf.Col(); ln != 0 || c != 3 {
		t.Fatalf("caret = (%d,%d), want it left at (0,3)", ln, c)
	}
	mustText(t, m.cur().buf.Text(), "plain text\n")
}

// TestBracketCommandsDoNotEditAndDoNotUndo: both are pure caret moves, so
// neither the text nor the undo history may change.
func TestBracketCommandsDoNotEditAndDoNotUndo(t *testing.T) {
	m := delimModel(t, "foo(bar)\n")

	place(m, 0, 5)
	m = press(m, jumpChord)
	m = press(m, selectChord)
	mustText(t, m.cur().buf.Text(), "foo(bar)\n")
	if m.cur().buf.Dirty() {
		t.Fatal("navigating must not make the buffer dirty")
	}
	if m.cur().buf.Undo() {
		t.Fatal("neither command may push an undo step")
	}
}

func TestBracketCommandsClearSecondaryCursors(t *testing.T) {
	m := delimModel(t, "f(baz) g(baz)\n")
	place(m, 0, 3)
	m = press(m, altDChord)
	m = press(m, altDChord)
	if !m.cur().buf.HasMultipleCursors() {
		t.Skip("the fixture did not arm two cursors")
	}
	m = press(m, jumpChord)
	mustBool(t, "secondary cursors dropped", m.cur().buf.HasMultipleCursors(), false)

	m = delimModel(t, "f(baz) g(baz)\n")
	place(m, 0, 3)
	m = press(m, altDChord)
	m = press(m, altDChord)
	m = press(m, selectChord)
	mustBool(t, "secondary cursors dropped", m.cur().buf.HasMultipleCursors(), false)
}

func TestBracketPaletteCommands(t *testing.T) {
	m := delimModel(t, "foo(bar)\n")
	place(m, 0, 7) // on the closer, so the jump has somewhere to go
	m = press(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if !m.paletteOpen {
		t.Fatal("the palette must open")
	}
	seen := map[string]bool{}
	for _, hit := range m.filterPalette() {
		seen[hit.id] = true
	}
	for _, id := range []string{"jump_bracket", "select_inside_brackets"} {
		if !seen[id] {
			t.Fatalf("the palette must offer %q, got %v", id, paletteIDs(m.filterPalette()))
		}
	}

	// The filter finds them and the palette route moves the caret like the chord.
	m = typeStr(m, "bracket")
	hits := m.filterPalette()
	idx := -1
	for i, hit := range hits {
		if hit.id == "jump_bracket" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("filtering on \"bracket\" must find the jump command, got %v", paletteIDs(hits))
	}
	m.paletteSel = idx
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.paletteOpen {
		t.Fatal("executing a command closes the palette")
	}
	if _, c := m.cur().buf.CurLine(), m.cur().buf.Col(); c != 3 {
		t.Fatalf("palette jump left the caret at col %d, want the opener at 3", c)
	}

	viaChord := delimModel(t, "foo(bar)\n")
	place(viaChord, 0, 7)
	viaChord = press(viaChord, jumpChord)
	mustBool(t, "palette and chord agree",
		m.cur().buf.Col() == viaChord.cur().buf.Col(), true)
}

func TestHelpListsBracketRows(t *testing.T) {
	wantKeys := []string{"Ctrl+]", "Ctrl+Shift+]"}
	found := map[string]bool{}
	for _, e := range helpEntries {
		found[e.keys] = true
	}
	for _, k := range wantKeys {
		if !found[k] {
			t.Fatalf("helpEntries is missing the %q row", k)
		}
	}

	m := New()
	m.width, m.height = 120, 24
	m = press(m, tea.KeyPressMsg{Code: tea.KeyF1})
	// The rows are in the middle of the list, so scroll the window to them
	// rather than to the end of it.
	for i, e := range helpEntries {
		if e.keys == "Ctrl+]" {
			m.helpScroll = i
		}
	}
	out := m.View().Content
	for _, want := range []string{
		"Ctrl+]", "Ctrl+Shift+]", m.t("help.jump_bracket"), m.t("help.select_inside"),
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the help must show %q", want)
		}
	}
}
