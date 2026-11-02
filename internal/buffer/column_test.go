package buffer

import (
	"strings"
	"testing"
)

// setBlock arms a column selection the way GrowColumn does, without walking the
// chords: anchor at (fromLine, fromCol), caret corner at (toLine, toCol).
func setBlock(b *Buffer, fromLine, fromCol, toLine, toCol int) {
	b.SetCursor(fromLine, fromCol)
	b.goalCol = toCol
	if !b.GrowColumn(toLine-fromLine, toCol-fromCol) && (fromLine != toLine || fromCol != toCol) {
		b.SetCursor(fromLine, fromCol)
		b.hasSelection = true
		b.selLine, b.selCol = fromLine, fromCol
		b.line, b.col = toLine, toCol
		b.goalCol = toCol
		b.clampCol()
		b.colSel = true
	}
}

func TestColumnGrowFromCaret(t *testing.T) {
	b := Load("alpha\nbeta\ngamma\n")
	b.SetCursor(0, 2)
	if !b.GrowColumn(2, 3) {
		t.Fatal("grow refused")
	}
	if !b.IsColumnSelection() {
		t.Fatal("expected column mode after GrowColumn")
	}
	first, last := b.ColumnRows()
	if first != 0 || last != 2 {
		t.Fatalf("rows = %d..%d, want 0..2", first, last)
	}
	if f, to, ok := b.ColumnCell(0); !ok || f != 2 || to != 5 {
		t.Fatalf("cell on row 0 = %d..%d ok=%v, want 2..5", f, to, ok)
	}
	// "beta" ends inside the block: the cell clamps to the row's length while
	// the corner keeps the goal column.
	if f, to, ok := b.ColumnCell(1); !ok || f != 2 || to != 4 {
		t.Fatalf("cell on row 1 = %d..%d ok=%v, want 2..4", f, to, ok)
	}
}

func TestColumnGrowClampsAtEdges(t *testing.T) {
	b := Load("one\ntwo\n")
	setBlock(b, 0, 0, 1, 3)
	if b.GrowColumn(1, 0) {
		t.Fatal("grew past the last row")
	}
	if b.GrowColumn(-2, 0) {
		t.Fatal("grew past the first row")
	}
	// A shrink to the anchor itself still moves the caret (dRow/dCol applied),
	// but drops the selection: the block ends where it began.
	b.GrowColumn(-1, -3)
	if b.HasSelection() || b.IsColumnSelection() {
		t.Fatal("selection survived a shrink onto its anchor")
	}
}

func TestColumnCellClampsToRow(t *testing.T) {
	b := Load("ab\nabcde\ncd\n")
	setBlock(b, 0, 1, 2, 4)
	// Row 0 ends inside the block, row 2 is shorter than the left edge.
	if f, to, ok := b.ColumnCell(0); !ok || f != 1 || to != 2 {
		t.Fatalf("row 0 cell = %d..%d, want 1..2", f, to)
	}
	// Row 2 "cd" ends inside the block: its cell runs from the left edge (1)
	// to the row's end, clamped — the caret rests at the clamp, the goal
	// column keeps the corner.
	if f, to, ok := b.ColumnCell(2); !ok || f != 1 || to != 2 {
		t.Fatalf("row 2 cell = %d..%d ok=%v, want 1..2", f, to, ok)
	}
	if _, _, ok := b.ColumnCell(3); ok {
		t.Fatal("a row the rectangle does not cover has no cell")
	}
}

func TestInsertColumnTextSingleAndMulti(t *testing.T) {
	b := Load("one\n\nthree\n")
	setBlock(b, 0, 1, 2, 3)
	if !b.InsertColumnText("-X-") {
		t.Fatal("insert refused")
	}
	if got, want := b.Text(), "o-X-\n-X-\nt-X-ee\n"; got != want {
		t.Fatalf("single-line insert: %q, want %q", got, want)
	}
	b.Undo()
	if b.Text() != "one\n\nthree\n" {
		t.Fatalf("undo lost: %q", b.Text())
	}

	// Multi-line text: "A" to row 0, "B" to row 1. Row 2's cell is empty (the
	// blank middle row of the document is shorter than the block), so it gets
	// nothing — a row with no cell is not edited.
	setBlock(b, 0, 1, 2, 3)
	b.InsertColumnText("A\nB")
	if got, want := b.Text(), "oA\nB\nthree\n"; got != want {
		t.Fatalf("multi-line insert: %q", got)
	}
}

func TestColumnBackspaceAndDelete(t *testing.T) {
	// A stack of vertical carets (zero-width block): backspace takes the
	// character in front, delete the one after, each row on its own.
	b := Load("abc\nx\ndef\n")
	setBlock(b, 0, 2, 2, 2)
	if !b.ColumnBackspace() {
		t.Fatal("backspace refused")
	}
	if got, want := b.Text(), "ac\n\ndf\n"; got != want {
		t.Fatalf("column backspace: %q, want %q", got, want)
	}
	// Row 1 is empty and keeps nothing to remove in front; rows 0 and 2 lost
	// their second character.
	b.Undo()
	if b.Text() != "abc\nx\ndef\n" {
		t.Fatalf("undo lost: %q", b.Text())
	}

	b2 := Load("abc\nx\ndef\n")
	setBlock(b2, 0, 2, 2, 2)
	b2.ColumnDelete()
	if got, want := b2.Text(), "ab\nx\nde\n"; got != want {
		t.Fatalf("column delete: %q, want %q", got, want)
	}
}

func TestColumnDeleteSelectionWholeCells(t *testing.T) {
	b := Load("one-two\nab-cd\n")
	setBlock(b, 0, 1, 1, 4)
	if !b.DeleteSelection() {
		t.Fatal("delete refused")
	}
	if got, want := b.Text(), "otwo\nad\n"; got != want {
		t.Fatalf("delete cells: %q, want %q", got, want)
	}
	b.Undo()
	if b.Text() != "one-two\nab-cd\n" {
		t.Fatalf("undo lost: %q", b.Text())
	}
}

func TestMapColumnCells(t *testing.T) {
	b := Load("ab cd\nef\n")
	setBlock(b, 0, 0, 1, 2)
	if !b.MapColumnCells(strings.ToUpper) {
		t.Fatal("map reported no change")
	}
	if got, want := b.Text(), "AB cd\nEF\n"; got != want {
		t.Fatalf("mapped: %q, want %q", got, want)
	}
	// A second pass with nothing left to change pushes no undo step.
	if b.MapColumnCells(strings.ToUpper) {
		t.Fatal("no-op map reported change")
	}
}

func TestInsertColumnPasteUnfold(t *testing.T) {
	b := Load("row one\nrow two\nrow three\n")
	b.SetCursor(0, 3)
	if !b.InsertColumn([]string{"A", "B"}) {
		t.Fatal("insert refused")
	}
	if got, want := b.Text(), "rowA one\nrowB two\nrow three\n"; got != want {
		t.Fatalf("unfold paste: %q, want %q", got, want)
	}
	// Carets landed on every row the paste touched.
	if !b.HasMultipleCursors() {
		t.Fatal("expected a caret per touched row")
	}
	b.Undo()
	if b.Text() != "row one\nrow two\nrow three\n" {
		t.Fatalf("undo lost: %q", b.Text())
	}
}

func TestInsertColumnDropsRowsPastDocument(t *testing.T) {
	b := Load("a\nb\n")
	b.SetCursor(1, 0)
	if !b.InsertColumn([]string{"X", "Y", "Z"}) {
		t.Fatal("insert refused")
	}
	if got, want := b.Text(), "a\nXb\n"; got != want {
		t.Fatalf("clipped unfold: %q, want %q", got, want)
	}
}

func TestEnterColumnSplitsEveryRow(t *testing.T) {
	b := Load("one\nabc\n")
	setBlock(b, 0, 1, 1, 2)
	b.EnterColumn("  ")
	if got, want := b.Text(), "o\n  e\na\n  c\n"; got != want {
		t.Fatalf("enter column: %q, want %q", got, want)
	}
	b.Undo()
	if b.Text() != "one\nabc\n" {
		t.Fatalf("undo lost: %q", b.Text())
	}
}

func TestColumnTextAndSelectedText(t *testing.T) {
	b := Load("abcd\nefgh\n")
	setBlock(b, 0, 1, 1, 3)
	if got, want := b.ColumnText(), "bc\nfg"; got != want {
		t.Fatalf("ColumnText: %q, want %q", got, want)
	}
	if got, want := b.SelectedText(), "bc\nfg"; got != want {
		t.Fatalf("SelectedText: %q, want %q", got, want)
	}
}

func TestLinearSelectionClearsColumnFlag(t *testing.T) {
	b := Load("abc\ndef\nghi\n")
	setBlock(b, 0, 0, 1, 2)
	if !b.IsColumnSelection() {
		t.Fatal("setup failed")
	}
	b.StartSelection()
	if b.IsColumnSelection() {
		t.Fatal("StartSelection left the block rectangular")
	}
	b.SetColumnSelection(true)
	if !b.IsColumnSelection() {
		t.Fatal("toggle on failed")
	}
	b.DragSelect(2, 0)
	if b.IsColumnSelection() {
		t.Fatal("DragSelect left the block rectangular")
	}
}

func TestInsertTextIntoBlockReplacesCells(t *testing.T) {
	b := Load("one\ntwo\n")
	setBlock(b, 0, 1, 1, 2)
	b.InsertText("Z")
	if got, want := b.Text(), "oZe\ntZo\n"; got != want {
		t.Fatalf("type into block: %q, want %q", got, want)
	}
	if b.IsColumnSelection() {
		t.Fatal("block should be gone after typing; multi-cursor takes over")
	}
	b.Undo()
	if b.Text() != "one\ntwo\n" {
		t.Fatalf("undo lost: %q", b.Text())
	}
}

func TestColumnTypingBecomesMultiCursor(t *testing.T) {
	// After a column edit the block is gone and a caret per row remains: the
	// next keystroke is ordinary multi-cursor state.
	b := Load("aa\nbb\n")
	setBlock(b, 0, 1, 1, 1)
	b.InsertText("|")
	if got, want := b.Text(), "a|a\nb|b\n"; got != want {
		t.Fatalf("first keystroke: %q, want %q", got, want)
	}
	if !b.HasMultipleCursors() {
		t.Fatal("expected one caret per row after the block edit")
	}
	b.MultiInsertText("!")
	if got, want := b.Text(), "a|!a\nb|!b\n"; got != want {
		t.Fatalf("multi-cursor keystroke: %q, want %q", got, want)
	}
}

func TestApplyCaseOnBlock(t *testing.T) {
	b := Load("one two\nthree four\n")
	setBlock(b, 0, 0, 1, 3)
	b.ApplyCase(true)
	if got, want := b.Text(), "ONE two\nTHRee four\n"; got != want {
		t.Fatalf("upper block: %q, want %q", got, want)
	}
	b.ApplyCase(false)
	if got, want := b.Text(), "one two\nthree four\n"; got != want {
		t.Fatalf("lower block: %q, want %q", got, want)
	}
}

func TestGrowColumnToDrag(t *testing.T) {
	b := Load("abcdef\nxy\nmnopqr\n")
	b.SetCursor(1, 2)
	// First motion enters column mode from the caret.
	if !b.GrowColumnTo(0, 4) {
		t.Fatal("first drag motion refused")
	}
	if !b.IsColumnSelection() {
		t.Fatal("expected column mode")
	}
	if f, to, ok := b.ColumnCell(0); !ok || f != 2 || to != 4 {
		t.Fatalf("row 0 cell = %d..%d, want 2..4", f, to)
	}
	// Sweeping down a short row keeps the corner at the goal column.
	if !b.GrowColumnTo(2, 4) {
		t.Fatal("vertical sweep refused")
	}
	if f, to, ok := b.ColumnCell(1); ok || f != 2 || to != 2 {
		t.Fatalf("short row cell = %d..%d ok=%v, want empty at 2..2", f, to, ok)
	}
	// Dragging the corner back onto the anchor ends the block.
	b.GrowColumnTo(1, 2)
	if b.HasSelection() || b.IsColumnSelection() {
		t.Fatal("selection survived a drag onto its anchor")
	}
}

func TestColumnTransformKeepsCornerColumn(t *testing.T) {
	// commitRange must not narrow a rectangle's corner after a command that
	// permutes rows: the goal column may stand past the end of a short row.
	b := Load("abcdef\nxy\n")
	setBlock(b, 0, 0, 1, 6)
	b.SortLines(0, 1)
	if _, _, ok := b.ColumnCell(1); !ok {
		t.Fatal("short row lost its cell after a row permutation")
	}
	if f, to, ok := b.ColumnCell(0); !ok || f != 0 || to != 6 {
		t.Fatalf("row 0 cell = %d..%d ok=%v, want 0..6", f, to, ok)
	}
}
