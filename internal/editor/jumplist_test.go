package editor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/lsp"
)

// The jump list is pane state plus caret moves: the four producers (definition,
// project hit, go-to-line, debugger stop) record the position they leave,
// Ctrl+Alt+← / Ctrl+Alt+→ walk the two stacks, and nothing edits, dirties or
// pushes an undo step.

var (
	backChord = tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModCtrl | tea.ModAlt}
	fwdChord  = tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModCtrl | tea.ModAlt}
)

func jumpModel(t *testing.T, content string) Model {
	t.Helper()
	m := New(writeTemp(t, t.TempDir(), "jump.txt", content))
	m.width, m.height = 80, 24
	return m
}

// gotoLine jumps through the Ctrl+L prompt: the producer every test can drive
// without an LSP server, a debugger or a project scan.
func gotoLine(m Model, spec string) Model {
	m = press(m, tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	m = typeStr(m, spec)
	return press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
}

func caret(m Model) (int, int) { return m.cur().buf.CurLine(), m.cur().buf.Col() }

func TestJumpBackReturnsWhereTheJumpCameFrom(t *testing.T) {
	m := jumpModel(t, "aaa\nbbb\nccc\nddd\neee\n")
	place(m, 0, 2)
	m = gotoLine(m, "4")
	if ln, c := caret(m); ln != 3 || c != 0 {
		t.Fatalf("setup: caret = (%d,%d), want (3,0)", ln, c)
	}

	m = press(m, backChord)
	if ln, c := caret(m); ln != 0 || c != 2 {
		t.Fatalf("back: caret = (%d,%d), want the origin (0,2)", ln, c)
	}
	if m.msg != "" {
		t.Fatalf("a successful jump clears the status line, got %q", m.msg)
	}

	// Forward is the mirror: the same position the back left.
	m = press(m, fwdChord)
	if ln, c := caret(m); ln != 3 || c != 0 {
		t.Fatalf("forward: caret = (%d,%d), want (3,0)", ln, c)
	}
}

func TestJumpForwardIsTheMirror(t *testing.T) {
	m := jumpModel(t, "l0\nl1\nl2\nl3\nl4\n")
	place(m, 0, 0)
	m = gotoLine(m, "4") // from (0,0) to (3,0)
	m = gotoLine(m, "2") // from (3,0) to (1,0)

	m = press(m, backChord)
	if ln := m.cur().buf.CurLine(); ln != 3 {
		t.Fatalf("first back: line %d, want 3", ln)
	}
	m = press(m, backChord)
	if ln := m.cur().buf.CurLine(); ln != 0 {
		t.Fatalf("second back: line %d, want 0", ln)
	}
	if len(m.curPane().backs) != 0 {
		t.Fatalf("the back stack must be spent, got %v", m.curPane().backs)
	}

	m = press(m, fwdChord)
	if ln := m.cur().buf.CurLine(); ln != 3 {
		t.Fatalf("first forward: line %d, want 3", ln)
	}
	m = press(m, fwdChord)
	if ln := m.cur().buf.CurLine(); ln != 1 {
		t.Fatalf("second forward: line %d, want 1", ln)
	}
	m = press(m, fwdChord)
	if m.msg != "no later jump" {
		t.Fatalf("an exhausted forward stack must say so, got %q", m.msg)
	}
	if ln := m.cur().buf.CurLine(); ln != 1 {
		t.Fatalf("a refusal must leave the caret alone at line 1, got %d", ln)
	}
}

func TestDefinitionJumpPushes(t *testing.T) {
	dir := t.TempDir()
	a := writeTemp(t, dir, "a.txt", "aaa\nbbb\n")
	b := writeTemp(t, dir, "b.txt", "ccc\nddd\n")

	m := New(a)
	m.width, m.height = 80, 24
	place(m, 1, 1)

	res, _ := m.Update(lspDefinitionMsg{
		path: a,
		loc:  &lsp.Location{Path: b, Line: 0, Col: 1},
	})
	m = res.(Model)
	if m.cur().path != b {
		t.Fatalf("the definition jump must focus the target file, path=%q", m.cur().path)
	}
	if len(m.curPane().backs) != 1 {
		t.Fatalf("the definition jump pushes the position it leaves, backs=%v", m.curPane().backs)
	}

	m = press(m, backChord)
	if m.cur().path != a || m.activeTabIndex() != 0 {
		t.Fatalf("back crosses tabs: path=%q tab=%d", m.cur().path, m.activeTabIndex())
	}
	if ln, c := caret(m); ln != 1 || c != 1 {
		t.Fatalf("back: caret = (%d,%d), want (1,1)", ln, c)
	}

	m = press(m, fwdChord)
	if m.cur().path != b {
		t.Fatalf("forward returns to the definition file, path=%q", m.cur().path)
	}
	if ln, c := caret(m); ln != 0 || c != 1 {
		t.Fatalf("forward: caret = (%d,%d), want (0,1)", ln, c)
	}
}

func TestProjHitAndDapJumpsPush(t *testing.T) {
	dir := t.TempDir()
	a := writeTemp(t, dir, "a.txt", "aaa\nbbb\nccc\n")

	m := New(a)
	m.width, m.height = 80, 24
	m.root = dir
	place(m, 0, 1)
	m.openProjHit(projHit{path: "a.txt", line: 2, col: 0})
	if ln := m.cur().buf.CurLine(); ln != 2 {
		t.Fatalf("the hit jump moved the caret to line %d, want 2", ln)
	}
	if len(m.curPane().backs) != 1 {
		t.Fatalf("a hit accept pushes the position it leaves, backs=%v", m.curPane().backs)
	}
	m = press(m, backChord)
	if ln, c := caret(m); ln != 0 || c != 1 {
		t.Fatalf("back from a hit: caret = (%d,%d), want (0,1)", ln, c)
	}

	// The debugger's stop line is a jump the user never asked for by hand, so it
	// has to be walkable back.
	dap := New(writeTemp(t, dir, "d.txt", "x0\nx1\nx2\nx3\n"))
	dap.width, dap.height = 80, 24
	place(dap, 0, 2)
	dap.dapRunState = dapStopped
	dap.dapCurPath = dap.cur().path
	dap.dapCurLine = 3 // 1-based, as the adapter reports it
	dap.handleDapFollowMsg()
	if ln := dap.cur().buf.CurLine(); ln != 2 {
		t.Fatalf("the stop line must be revealed at line 2, got %d", ln)
	}
	if len(dap.curPane().backs) != 1 {
		t.Fatalf("following the stop line pushes the position it leaves, backs=%v", dap.curPane().backs)
	}
	dap = press(dap, backChord)
	if ln, c := caret(dap); ln != 0 || c != 2 {
		t.Fatalf("back from the stop line: caret = (%d,%d), want (0,2)", ln, c)
	}
}

func TestJumpHistoryDedupesTheSameSpot(t *testing.T) {
	m := jumpModel(t, "aaa\nbbb\nccc\n")
	place(m, 1, 1)
	m.pushJump()
	m.pushJump()
	if len(m.curPane().backs) != 1 {
		t.Fatalf("pushing the same position twice leaves one entry, got %v", m.curPane().backs)
	}
	place(m, 0, 0)
	m.pushJump()
	if len(m.curPane().backs) != 2 {
		t.Fatalf("a different position adds an entry, got %v", m.curPane().backs)
	}

	// The entry equal to where the caret already stands is skipped, so no back
	// press can be visibly a no-op.
	m = press(m, backChord)
	if ln, c := caret(m); ln != 1 || c != 1 {
		t.Fatalf("back must step past the duplicate to (1,1), got (%d,%d)", ln, c)
	}
	if m.msg != "" {
		t.Fatalf("a successful jump clears the status line, got %q", m.msg)
	}
}

func TestNewJumpClearsForward(t *testing.T) {
	m := jumpModel(t, "l0\nl1\nl2\n")
	place(m, 0, 0)
	m = gotoLine(m, "3")
	m = press(m, backChord)
	if len(m.curPane().forwards) != 1 {
		t.Fatalf("the back press fills the forward stack, got %v", m.curPane().forwards)
	}

	m = gotoLine(m, "2")
	if m.curPane().forwards != nil {
		t.Fatalf("a new jump abandons the branch not taken, got %v", m.curPane().forwards)
	}
	m = press(m, fwdChord)
	if m.msg != "no later jump" {
		t.Fatalf("forward after a new jump must refuse, got %q", m.msg)
	}
	// The position the new jump left is still walkable back.
	m = press(m, backChord)
	if ln := m.cur().buf.CurLine(); ln != 0 {
		t.Fatalf("back after the new jump: line %d, want 0", ln)
	}
}

func TestJumpHistoryIsCapped(t *testing.T) {
	m := jumpModel(t, "l0\nl1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\n")
	for i := 0; i < 100; i++ {
		place(m, i%10, 0)
		m.pushJump()
	}
	p := m.curPane()
	if len(p.backs) != jumpHistoryLen {
		t.Fatalf("the back stack is capped at %d, got %d", jumpHistoryLen, len(p.backs))
	}
	if got, want := p.backs[0].line, (100-jumpHistoryLen)%10; got != want {
		t.Fatalf("the oldest entries drop off the front: first line %d, want %d", got, want)
	}
	if got := p.backs[len(p.backs)-1].line; got != 99%10 {
		t.Fatalf("the newest entry must be the last push, got line %d", got)
	}
}

func TestJumpStacksLiveInThePane(t *testing.T) {
	m := jumpModel(t, "aaa\nbbb\nccc\n")
	place(m, 2, 1)
	m = gotoLine(m, "1")
	if len(m.curPane().backs) != 1 {
		t.Fatalf("setup: backs=%v", m.curPane().backs)
	}

	m.splitVert() // the new pane becomes active, with its own empty history
	if m.activePane != 1 {
		t.Fatalf("setup: activePane=%d", m.activePane)
	}
	m = press(m, backChord)
	if m.msg != "no earlier jump" {
		t.Fatalf("a fresh pane has no history, got %q", m.msg)
	}
	if ln := m.cur().buf.CurLine(); ln != 0 {
		t.Fatalf("the other pane's history must not be spent here, line %d", ln)
	}

	m.focusOtherPane()
	m = press(m, backChord)
	if ln, c := caret(m); ln != 2 || c != 1 {
		t.Fatalf("back in the owning pane: caret = (%d,%d), want (2,1)", ln, c)
	}
}

func TestJumpReopensAClosedTab(t *testing.T) {
	dir := t.TempDir()
	a := writeTemp(t, dir, "a.txt", "aaa\nbbb\n")
	b := writeTemp(t, dir, "b.txt", "ccc\nddd\n")

	m := New(a, b)
	m.width, m.height = 80, 24
	place(m, 1, 1) // in b.txt, the tab New() puts on screen
	if m.cur().path != b {
		t.Fatalf("setup: path=%q", m.cur().path)
	}
	m.pushJump() // record where a jump is leaving b.txt
	m.setActiveTab(0)
	m.pushJump()    // and where the jump back into a.txt came from
	m.closeTabAt(1) // b.txt is clean, so it closes without a prompt
	if len(m.tabs) != 1 {
		t.Fatalf("setup: tabs=%d", len(m.tabs))
	}

	// The history still names b.txt; back reopens it from disk.
	m = press(m, backChord)
	if m.cur().path != b {
		t.Fatalf("back must reopen the closed file, path=%q want %q", m.cur().path, b)
	}
	if ln, c := caret(m); ln != 1 || c != 1 {
		t.Fatalf("back into the reopened tab: caret = (%d,%d), want (1,1)", ln, c)
	}
	if len(m.tabs) != 2 {
		t.Fatalf("the reopened file is a tab again, tabs=%d", len(m.tabs))
	}
}

func TestJumpBackOnEmptyHistoryLeavesTheCaretAlone(t *testing.T) {
	m := jumpModel(t, "aaa\nbbb\n")
	place(m, 1, 2)
	m = press(m, backChord)
	if m.msg != "no earlier jump" {
		t.Fatalf("empty history must say so, got %q", m.msg)
	}
	if ln, c := caret(m); ln != 1 || c != 2 {
		t.Fatalf("a refusal must not move the caret, got (%d,%d)", ln, c)
	}
	m = press(m, fwdChord)
	if m.msg != "no later jump" {
		t.Fatalf("an empty forward stack must say so, got %q", m.msg)
	}
	if len(m.curPane().backs) != 0 || len(m.curPane().forwards) != 0 {
		t.Fatalf("refusals push nothing: backs=%v forwards=%v", m.curPane().backs, m.curPane().forwards)
	}
}

func TestJumpCommandsDoNotEditAndDoNotUndo(t *testing.T) {
	m := jumpModel(t, "aaa\nbbb\nccc\n")
	place(m, 0, 1)
	before := m.cur().buf.Text()
	m = gotoLine(m, "3")
	m = press(m, backChord)
	m = press(m, fwdChord)
	mustText(t, m.cur().buf.Text(), before)
	if m.cur().buf.Dirty() {
		t.Fatal("navigating must not make the buffer dirty")
	}
	if m.cur().buf.Undo() {
		t.Fatal("neither jump nor back may push an undo step")
	}
}

func TestJumpCommandsClearSecondaryCursors(t *testing.T) {
	m := jumpModel(t, "aaa bbb aaa bbb\nccc\n")
	place(m, 0, 0)
	m = press(m, altDChord)
	m = press(m, altDChord)
	if !m.cur().buf.HasMultipleCursors() {
		t.Skip("the fixture did not arm two cursors")
	}
	m = gotoLine(m, "2")
	m = press(m, backChord)
	mustBool(t, "secondary cursors dropped on back", m.cur().buf.HasMultipleCursors(), false)
	m = press(m, fwdChord)
	mustBool(t, "secondary cursors dropped on forward", m.cur().buf.HasMultipleCursors(), false)
}

func TestJumpListPaletteCommands(t *testing.T) {
	m := jumpModel(t, "aaa\nbbb\nccc\n")
	place(m, 0, 1)
	m = gotoLine(m, "3")

	m = press(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if !m.paletteOpen {
		t.Fatal("the palette must open")
	}
	seen := map[string]bool{}
	for _, hit := range m.filterPalette() {
		seen[hit.id] = true
	}
	for _, id := range []string{"jump_back", "jump_forward"} {
		if !seen[id] {
			t.Fatalf("the palette must offer %q, got %v", id, paletteIDs(m.filterPalette()))
		}
	}

	m = typeStr(m, "jump")
	hits := m.filterPalette()
	if len(hits) < 2 || hits[0].id != "jump_back" || hits[1].id != "jump_forward" {
		t.Fatalf("filtering on \"jump\" must put the two jump-list commands first, got %v", paletteIDs(hits))
	}
	m.paletteSel = 0
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.paletteOpen {
		t.Fatal("executing a command closes the palette")
	}
	if ln, c := caret(m); ln != 0 || c != 1 {
		t.Fatalf("the palette route left the caret at (%d,%d), want the origin (0,1)", ln, c)
	}

	viaChord := jumpModel(t, "aaa\nbbb\nccc\n")
	place(viaChord, 0, 1)
	viaChord = gotoLine(viaChord, "3")
	viaChord = press(viaChord, backChord)
	pLine, pCol := caret(m)
	cLine, cCol := caret(viaChord)
	if pLine != cLine || pCol != cCol {
		t.Fatalf("palette and chord disagree: palette (%d,%d), chord (%d,%d)", pLine, pCol, cLine, cCol)
	}
}

func TestHelpListsJumpListRow(t *testing.T) {
	wantKey := "Ctrl+Alt+← / →"
	found := false
	for _, e := range helpEntries {
		if e.keys == wantKey {
			found = true
		}
	}
	if !found {
		t.Fatalf("helpEntries is missing the %q row", wantKey)
	}

	m := jumpModel(t, "aaa\n")
	m.width, m.height = 120, 24 // the row's description is wide; see TestHelpListsBracketRows
	m = press(m, tea.KeyPressMsg{Code: tea.KeyF1})
	// The row sits in the middle of the list, so scroll the window to it rather
	// than to the end of it.
	for i, e := range helpEntries {
		if e.keys == wantKey {
			m.helpScroll = i
		}
	}
	out := m.View().Content
	for _, want := range []string{wantKey, m.t("help.jump_list")} {
		if !strings.Contains(out, want) {
			t.Fatalf("the help must show %q", want)
		}
	}
}
