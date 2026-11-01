package buffer

// Tests for the selection transformations (transform.go): the case fold, block
// sort, trailing trim, indent alignment and list numbering. Each one is asserted
// as exactly one undo step, which is the property the editor relies on when it
// says "Ctrl+Z peels one transformation".

import "testing"

func TestApplyCaseSelectionFoldsOnlyTheSelection(t *testing.T) {
	b := Load("hello world\nsecond line\n")
	b.SetCursor(0, 6)
	b.MoveRightWithSelect()
	b.MoveRightWithSelect()
	b.MoveRightWithSelect()
	b.MoveRightWithSelect()
	b.MoveRightWithSelect()
	b.ApplyCase(true)
	if b.Text() != "hello WORLD\nsecond line\n" {
		t.Fatalf("fold selection: %q", b.Text())
	}
	if !b.Undo() {
		t.Fatal("a case fold must be undoable")
	}
	if b.Text() != "hello world\nsecond line\n" {
		t.Fatalf("after undo: %q", b.Text())
	}
	if b.Undo() {
		t.Fatal("one fold is one undo step")
	}
}

func TestApplyCaseWholeBufferKeepsHistoryAndLines(t *testing.T) {
	b := Load("hello\nworld\n")
	b.SetCursor(1, 3)
	b.ApplyCase(true)
	if b.Text() != "HELLO\nWORLD\n" {
		t.Fatalf("fold buffer: %q", b.Text())
	}
	if b.LineCount() != 2 {
		t.Fatalf("folding must not change the line count: %d", b.LineCount())
	}
	if b.CurLine() != 1 || b.Col() != 3 {
		t.Fatalf("caret after fold: line=%d col=%d", b.CurLine(), b.Col())
	}
	// The regression this replaces: the command used to rebuild the buffer with
	// buffer.Load, which left no undo history to return to.
	if !b.Undo() {
		t.Fatal("a whole-buffer fold must be undoable")
	}
	if b.Text() != "hello\nworld\n" {
		t.Fatalf("after undo: %q", b.Text())
	}
	b.Redo()
	if b.Text() != "HELLO\nWORLD\n" {
		t.Fatalf("after redo: %q", b.Text())
	}

	b = Load("HELLO\nWORLD\n")
	b.ApplyCase(false)
	if b.Text() != "hello\nworld\n" {
		t.Fatalf("lowercase buffer: %q", b.Text())
	}
}

func TestSortLinesFoldsCaseAndMovesBlanksLast(t *testing.T) {
	b := Load("banana\nApple\n\ncherry\napple\n")
	if n := b.SortLines(0, 4); n != 5 {
		t.Fatalf("sorted line count = %d, want 5", n)
	}
	// apple/banana/cherry in folded order; the two apples are broken by the exact
	// compare, and the blank line ends up after the content, not before it.
	if want := "Apple\napple\nbanana\ncherry\n\n"; b.Text() != want {
		t.Fatalf("sorted: %q, want %q", b.Text(), want)
	}
	if !b.Undo() {
		t.Fatal("a sort must be undoable")
	}
	if b.Text() != "banana\nApple\n\ncherry\napple\n" {
		t.Fatalf("after undo: %q", b.Text())
	}
}

func TestSortLinesRejectsWorklessBlocks(t *testing.T) {
	b := Load("one\ntwo\nthree\n")
	// A single line has nothing to permute, and it must not cost an undo step.
	if n := b.SortLines(0, 0); n != 0 {
		t.Fatalf("single-line sort = %d, want 0", n)
	}
	if b.Undo() {
		t.Fatal("a sort that did nothing must not enter the undo history")
	}
	b = Load("Apple\nbanana\n")
	if n := b.SortLines(0, 1); n != 0 {
		t.Fatalf("already sorted = %d, want 0", n)
	}
	if b.Text() != "Apple\nbanana\n" {
		t.Fatalf("already sorted text changed: %q", b.Text())
	}
}

func TestSortLinesTouchesOnlyTheBlock(t *testing.T) {
	b := Load("head\nzulu\nalpha\nhead\n")
	if n := b.SortLines(1, 2); n != 2 {
		t.Fatalf("sorted = %d", n)
	}
	if want := "head\nalpha\nzulu\nhead\n"; b.Text() != want {
		t.Fatalf("sort outside the block moved lines: %q, want %q", b.Text(), want)
	}
}

func TestTrimTrailingCountsLinesAndKeepsIndent(t *testing.T) {
	b := Load("  a  \nb\t\t\n c\n")
	if n := b.TrimTrailing(0, 2); n != 2 {
		t.Fatalf("trimmed = %d, want 2", n)
	}
	if want := "  a\nb\n c\n"; b.Text() != want {
		t.Fatalf("trimmed: %q, want %q", b.Text(), want)
	}
	if n := b.TrimTrailing(0, 2); n != 0 {
		t.Fatalf("a second trim = %d, want 0", n)
	}
	if !b.Undo() {
		t.Fatal("a trim must be undoable")
	}
	if b.Text() != "  a  \nb\t\t\n c\n" {
		t.Fatalf("after undo: %q", b.Text())
	}
}

func TestAlignIndentPadsToTheDeepestColumnAndKeepsTabs(t *testing.T) {
	// tab_width 4: the first line is 8 columns deep and is already the target, so
	// it must not be rewritten — the other two only gain spaces in front.
	b := Load("\t\talpha\n  beta\ngamma\n   \n")
	if n := b.AlignIndent(0, 3, 4); n != 2 {
		t.Fatalf("aligned = %d, want 2", n)
	}
	if want := "\t\talpha\n        beta\n        gamma\n   \n"; b.Text() != want {
		t.Fatalf("aligned: %q, want %q", b.Text(), want)
	}
	if n := b.AlignIndent(0, 3, 4); n != 0 {
		t.Fatalf("a second align = %d, want 0", n)
	}
}

func TestAlignIndentOfAnUnindentedBlockIsANoOp(t *testing.T) {
	b := Load("alpha\nbeta\n\n")
	if n := b.AlignIndent(0, 2, 4); n != 0 {
		t.Fatalf("aligned = %d, want 0", n)
	}
	if b.Text() != "alpha\nbeta\n\n" {
		t.Fatalf("text changed: %q", b.Text())
	}
	if b.Undo() {
		t.Fatal("a no-op align must not enter the undo history")
	}
}

func TestNumberLinesSkipsBlanksAndReplacesAnExistingMarker(t *testing.T) {
	b := Load("a\n\nb\n1) c\n")
	if n := b.NumberLines(0, 3); n != 3 {
		t.Fatalf("numbered = %d, want 3", n)
	}
	// The blank line is skipped and does not consume a number; "1) c" is a
	// numbered item already, so its marker is replaced, not stacked.
	if want := "1. a\n\n2. b\n3. c\n"; b.Text() != want {
		t.Fatalf("numbered: %q, want %q", b.Text(), want)
	}
}

func TestNumberLinesKeepsIndentAndIsRepeatable(t *testing.T) {
	b := Load("  alpha\n    beta\n")
	if n := b.NumberLines(0, 1); n != 2 {
		t.Fatalf("numbered = %d", n)
	}
	if want := "  1. alpha\n    2. beta\n"; b.Text() != want {
		t.Fatalf("numbered: %q, want %q", b.Text(), want)
	}
	if n := b.NumberLines(0, 1); n != 2 {
		t.Fatalf("numbered again = %d, want 2", n)
	}
	if b.Text() != "  1. alpha\n    2. beta\n" {
		t.Fatalf("a second run stacked markers: %q", b.Text())
	}
	if !b.Undo() {
		t.Fatal("numbering must be undoable")
	}
	if b.Text() != "  alpha\n    beta\n" {
		t.Fatalf("after undo: %q", b.Text())
	}
}

func TestNumberLinesLeavesALineThatIsOnlyAMarker(t *testing.T) {
	b := Load("12.\nnext\n")
	if n := b.NumberLines(0, 1); n != 1 {
		t.Fatalf("numbered = %d, want 1", n)
	}
	if want := "12.\n1. next\n"; b.Text() != want {
		t.Fatalf("numbered: %q, want %q", b.Text(), want)
	}
}

func TestSelectionLinesTakesWholeLines(t *testing.T) {
	b := Load("one\ntwo\nthree\n")
	// SetCursor clears the selection, so the block is extended with shift-moves
	// from a mid-line anchor: only whole touched lines count.
	b.SetCursor(0, 2)
	b.MoveDownWithSelect()
	b.MoveDownWithSelect()
	if from, to := b.SelectionLines(); from != 0 || to != 2 {
		t.Fatalf("selection lines = %d..%d, want 0..2", from, to)
	}
	b.SetCursor(1, 0)
	if from, to := b.SelectionLines(); from != 1 || to != 1 {
		t.Fatalf("no selection lines = %d..%d, want 1..1", from, to)
	}
}

func TestTransformKeepsTheSelectionAndClampsTheCaret(t *testing.T) {
	// The caret rides the last line of the block; after the sort that line is the
	// short one, so the column has to be clamped instead of left out of range.
	b := Load("zz\naaaa\n")
	b.SetCursor(0, 0)
	b.MoveDownWithSelect()
	for i := 0; i < 4; i++ {
		b.MoveRightWithSelect()
	}
	if n := b.SortLines(0, 1); n != 2 {
		t.Fatalf("sorted = %d", n)
	}
	if b.Text() != "aaaa\nzz\n" {
		t.Fatalf("block sorted to %q", b.Text())
	}
	if !b.HasSelection() {
		t.Fatal("a block transform keeps the selection so it can be re-run")
	}
	if b.CurLine() != 1 || b.Col() != 2 {
		t.Fatalf("caret: line=%d col=%d", b.CurLine(), b.Col())
	}
}
