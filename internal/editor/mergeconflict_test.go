package editor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Merge-conflict fixtures are the bytes git writes: markers in column 0, the
// label after a space, and a trailing newline (the buffer keeps that shape).

const conflictTwo = `line1
<<<<<<< HEAD
ours one
=======
theirs one
>>>>>>> feature
line2
<<<<<<< HEAD
ours two
=======
theirs two
>>>>>>> other
line3
`

const conflictDiff3 = `<<<<<<< HEAD
ours
||||||| merged common ancestors
base
=======
theirs
>>>>>>> feature
tail
`

const conflictDeleted = `keep
<<<<<<< HEAD
=======
incoming
>>>>>>> feature
tail
`

const conflictBroken = `head
<<<<<<< HEAD
orphan text
tail
`

const conflictBrokenThenGood = `<<<<<<< HEAD
broken
plain
<<<<<<< HEAD
ours
=======
theirs
>>>>>>> end
tail
`

func conflictLinesOf(s string) []string { return strings.Split(s, "\n") }

func conflictModel(t *testing.T, content string, line int) Model {
	t.Helper()
	m := New(writeTemp(t, t.TempDir(), "merged.txt", content))
	m.width, m.height = 120, 30
	if m.cur() == nil {
		t.Fatal("the conflicted file must be open")
	}
	m.cur().buf.SetCursor(line, 0)
	return m
}

func TestScanMergeConflicts(t *testing.T) {
	blocks, broken := scanConflicts(conflictLinesOf(conflictTwo))
	if len(blocks) != 2 || len(broken) != 0 {
		t.Fatalf("two-sided file: blocks=%v broken=%v", blocks, broken)
	}
	if blocks[0] != (conflictBlock{start: 1, base: conflictAll, sep: 3, end: 5}) {
		t.Fatalf("first block = %+v", blocks[0])
	}
	if blocks[1] != (conflictBlock{start: 7, base: conflictAll, sep: 9, end: 11}) {
		t.Fatalf("second block = %+v", blocks[1])
	}

	blocks, broken = scanConflicts(conflictLinesOf(conflictDiff3))
	if len(blocks) != 1 || len(broken) != 0 {
		t.Fatalf("diff3 file: blocks=%v broken=%v", blocks, broken)
	}
	if blocks[0] != (conflictBlock{start: 0, base: 2, sep: 4, end: 6}) {
		t.Fatalf("diff3 block = %+v", blocks[0])
	}

	// A marker that never closed is reported, never turned into a block.
	blocks, broken = scanConflicts(conflictLinesOf(conflictBroken))
	if len(blocks) != 0 || len(broken) != 1 || broken[0] != 1 {
		t.Fatalf("broken file: blocks=%v broken=%v", blocks, broken)
	}

	// A conflict never nests: the second start ends the first block reading.
	blocks, broken = scanConflicts(conflictLinesOf(conflictBrokenThenGood))
	if len(broken) != 1 || broken[0] != 0 {
		t.Fatalf("nested starts: broken=%v", broken)
	}
	if len(blocks) != 1 || blocks[0] != (conflictBlock{start: 3, base: conflictAll, sep: 5, end: 7}) {
		t.Fatalf("nested starts: blocks=%v", blocks)
	}
}

func TestScanMergeConflictsIgnoresLookalikes(t *testing.T) {
	lines := conflictLinesOf(`title
=======
indented
    <<<<<<< HEAD
eight
<<<<<<<<
done
`)
	if blocks, broken := scanConflicts(lines); len(blocks) != 0 || len(broken) != 0 {
		t.Fatalf("a setext underline, an indented marker and an eight-character run are not conflicts: blocks=%v broken=%v", blocks, broken)
	}
	if isMarkSep("======= extra") || isMarkSep(strings.Repeat("=", 8)) {
		t.Fatal("only a bare seven-character separator counts")
	}
}

func TestMergeConflictSides(t *testing.T) {
	lines := conflictLinesOf(conflictDiff3)
	blocks, _ := scanConflicts(lines)
	if len(blocks) != 1 {
		t.Fatalf("the diff3 fixture must yield one block, got %v", blocks)
	}
	blk := blocks[0]
	if got := strings.Join(blk.side(lines, conflictOurs), "|"); got != "ours" {
		t.Fatalf("ours = %q", got)
	}
	if got := strings.Join(blk.side(lines, conflictTheirs), "|"); got != "theirs" {
		t.Fatalf("theirs = %q", got)
	}
	if got := strings.Join(blk.side(lines, conflictBoth), "|"); got != "ours|theirs" {
		t.Fatalf("both = %q", got)
	}
	for _, k := range []conflictSide{conflictOurs, conflictTheirs, conflictBoth} {
		if strings.Contains(strings.Join(blk.side(lines, k), "\n"), "base") {
			t.Fatalf("side %d must drop the diff3 base section", k)
		}
	}

	lines = conflictLinesOf(conflictDeleted)
	blocks, _ = scanConflicts(lines)
	if len(blocks) != 1 {
		t.Fatalf("the deletion fixture must yield one block, got %v", blocks)
	}
	blk = blocks[0]
	if got := blk.side(lines, conflictOurs); len(got) != 0 {
		t.Fatalf("the deleted side must be empty, got %q", got)
	}
	if got := strings.Join(blk.side(lines, conflictBoth), "|"); got != "incoming" {
		t.Fatalf("both of a deletion conflict = %q", got)
	}
}

func TestResolveMergeConflictIsOneUndoStep(t *testing.T) {
	m := conflictModel(t, conflictTwo, 2)
	m = press(m, tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl | tea.ModShift})

	want := `line1
ours one
line2
<<<<<<< HEAD
ours two
=======
theirs two
>>>>>>> other
line3
`
	if got := m.cur().buf.Text(); got != want {
		t.Fatalf("take ours left %q", got)
	}
	// The cursor moves to the conflict that is left, the way every other editor
	// walks a conflict list.
	if got := m.cur().buf.CurLine(); got != 3 {
		t.Fatalf("cursor after resolve = %d, want the next block start", got)
	}
	if m.msg != "conflict resolved, 1 left" {
		t.Fatalf("msg = %q", m.msg)
	}
	if !m.cur().buf.Undo() {
		t.Fatal("one Ctrl+Z must undo the resolution")
	}
	if got := m.cur().buf.Text(); got != conflictTwo {
		t.Fatalf("undo left %q", got)
	}
	if m.cur().buf.Undo() {
		t.Fatal("the resolution was one undo step, not several")
	}
}

func TestResolveMergeConflictTheirsAndBoth(t *testing.T) {
	m := conflictModel(t, conflictTwo, 8)
	m = press(m, tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl | tea.ModShift})
	want := `line1
<<<<<<< HEAD
ours one
=======
theirs one
>>>>>>> feature
line2
theirs two
line3
`
	if got := m.cur().buf.Text(); got != want {
		t.Fatalf("take theirs left %q", got)
	}

	m = conflictModel(t, conflictTwo, 4)
	m = press(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl | tea.ModShift})
	want = `line1
ours one
theirs one
line2
<<<<<<< HEAD
ours two
=======
theirs two
>>>>>>> other
line3
`
	if got := m.cur().buf.Text(); got != want {
		t.Fatalf("take both left %q", got)
	}
	if m.msg != "conflict resolved, 1 left" {
		t.Fatalf("msg = %q", m.msg)
	}
}

func TestResolveMergeConflictDropsTheDiff3Base(t *testing.T) {
	m := conflictModel(t, conflictDiff3, 3)
	m = press(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl | tea.ModShift})
	want := "ours\ntheirs\ntail\n"
	if got := m.cur().buf.Text(); got != want {
		t.Fatalf("both of a diff3 conflict left %q", got)
	}
}

func TestResolveMergeConflictEmptySideLeavesNoBlankLine(t *testing.T) {
	m := conflictModel(t, conflictDeleted, 1)
	m = press(m, tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl | tea.ModShift})
	if got := m.cur().buf.Text(); got != "keep\ntail\n" {
		t.Fatalf("a deleted side must remove the block cleanly, got %q", got)
	}
	if m.msg != "no merge conflicts left — save (Ctrl+S) and stage the file" {
		t.Fatalf("msg = %q", m.msg)
	}

	m = conflictModel(t, conflictDeleted, 3)
	m = press(m, tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl | tea.ModShift})
	if got := m.cur().buf.Text(); got != "keep\nincoming\ntail\n" {
		t.Fatalf("take theirs left %q", got)
	}
}

func TestResolveMergeConflictBlockAtEndOfBuffer(t *testing.T) {
	// No trailing newline in the file: the >>>>>>> line is the buffer's last line,
	// so there is no following line for an empty side to merge into. The buffer
	// still ends with a newline, as every dmed buffer does.
	m := conflictModel(t, "<<<<<<< HEAD\na\n=======\nb\n>>>>>>> x", 1)
	m = press(m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl | tea.ModShift})
	if got := m.cur().buf.Text(); got != "a\nb\n" {
		t.Fatalf("last-line block left %q", got)
	}
	m = conflictModel(t, "<<<<<<< HEAD\n=======\n>>>>>>> x", 1)
	m = press(m, tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl | tea.ModShift})
	if got := m.cur().buf.Text(); got != "\n" {
		t.Fatalf("an empty side at the end of the buffer left %q", got)
	}
}

func TestResolveMergeConflictRefusals(t *testing.T) {
	m := conflictModel(t, conflictBroken, 2)
	m = press(m, tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl | tea.ModShift})
	if got := m.cur().buf.Text(); got != conflictBroken {
		t.Fatalf("an unterminated marker must not be edited, got %q", got)
	}
	if m.msg != "merge markers do not form a block — fix them by hand" {
		t.Fatalf("msg = %q", m.msg)
	}

	m = conflictModel(t, conflictBroken, 0)
	m = press(m, tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl | tea.ModShift})
	if m.msg != "no merge conflict under the cursor" {
		t.Fatalf("a cursor above the marker is not inside it, msg = %q", m.msg)
	}
	if got := m.cur().buf.Text(); got != conflictBroken {
		t.Fatalf("buffer changed by a refusal: %q", got)
	}

	// A broken marker at the top must not stop the user from resolving the
	// well-formed block below it.
	m = conflictModel(t, conflictBrokenThenGood, 4)
	m = press(m, tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl | tea.ModShift})
	want := `<<<<<<< HEAD
broken
plain
theirs
tail
`
	if got := m.cur().buf.Text(); got != want {
		t.Fatalf("resolving below a broken marker left %q", got)
	}
}

func TestJumpConflictWinsOverHunks(t *testing.T) {
	m := conflictModel(t, conflictTwo, 0)

	m = press(m, tea.KeyPressMsg{Code: ']', Mod: tea.ModAlt})
	if got := m.cur().buf.CurLine(); got != 1 {
		t.Fatalf("alt+] from the top landed on %d", got)
	}
	if m.msg != "merge conflict 1 of 2" {
		t.Fatalf("msg = %q", m.msg)
	}
	m = press(m, tea.KeyPressMsg{Code: ']', Mod: tea.ModAlt})
	if got := m.cur().buf.CurLine(); got != 7 || m.msg != "merge conflict 2 of 2" {
		t.Fatalf("second jump: line=%d msg=%q", got, m.msg)
	}
	// Past the last conflict the jump wraps around, like the hunk jump does.
	m = press(m, tea.KeyPressMsg{Code: ']', Mod: tea.ModAlt})
	if got := m.cur().buf.CurLine(); got != 1 || m.msg != "merge conflict 1 of 2" {
		t.Fatalf("wrap forward: line=%d msg=%q", got, m.msg)
	}
	m = press(m, tea.KeyPressMsg{Code: '[', Mod: tea.ModAlt})
	if got := m.cur().buf.CurLine(); got != 7 || m.msg != "merge conflict 2 of 2" {
		t.Fatalf("alt+[ wraps back: line=%d msg=%q", got, m.msg)
	}

	// Without markers the same chords are still the Git hunk jump.
	clean := New(writeTemp(t, t.TempDir(), "clean.txt", "one\ntwo\n"))
	clean.width, clean.height = 120, 30
	if clean.jumpConflict(1) {
		t.Fatal("a clean buffer has no conflict to jump to")
	}
	clean = press(clean, tea.KeyPressMsg{Code: ']', Mod: tea.ModAlt})
	if clean.msg != "no git changes" {
		t.Fatalf("the chord must fall through to the hunk jump, msg = %q", clean.msg)
	}
}

func TestMergeConflictHintOnOpen(t *testing.T) {
	m := New(writeTemp(t, t.TempDir(), "merged.txt", conflictTwo))
	if !strings.Contains(m.msg, "2 merge conflict(s)") {
		t.Fatalf("opening a conflicted file must say so, msg = %q", m.msg)
	}
	if !strings.Contains(m.msg, "Ctrl+Shift+O/T/B") {
		t.Fatalf("the hint must name the keys, msg = %q", m.msg)
	}

	m = New(writeTemp(t, t.TempDir(), "broken.txt", conflictBroken))
	if !strings.Contains(m.msg, "do not form a block") {
		t.Fatalf("a half-edited marker block must be reported, msg = %q", m.msg)
	}

	m = New(writeTemp(t, t.TempDir(), "clean.txt", "one\ntwo\n"))
	if strings.Contains(m.msg, "merge conflict") {
		t.Fatalf("a clean file must not claim conflicts, msg = %q", m.msg)
	}
}

func TestMergeConflictPaletteCommands(t *testing.T) {
	m := conflictModel(t, conflictTwo, 2)
	m = press(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if !m.paletteOpen {
		t.Fatal("the palette must open")
	}
	m = typeStr(m, "merge")
	seen := map[string]bool{}
	for i, hit := range m.filterPalette() {
		seen[hit.id] = true
		if hit.id == "merge_theirs" {
			m.paletteSel = i
		}
	}
	for _, id := range []string{"merge_ours", "merge_theirs", "merge_both"} {
		if !seen[id] {
			t.Fatalf("the palette must offer %q, got %v", id, paletteIDs(m.filterPalette()))
		}
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.paletteOpen {
		t.Fatal("executing a command closes the palette")
	}
	want := `line1
theirs one
line2
<<<<<<< HEAD
ours two
=======
theirs two
>>>>>>> other
line3
`
	if got := m.cur().buf.Text(); got != want {
		t.Fatalf("the palette route must resolve like the chord, got %q", got)
	}
}
