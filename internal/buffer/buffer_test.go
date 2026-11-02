package buffer

import "testing"

func TestEmpty(t *testing.T) {
	b := New()
	if b.Text() != "\n" {
		t.Fatalf("Text() = %q", b.Text())
	}
	if b.Dirty() {
		t.Fatal("fresh buffer must not be dirty")
	}
}

func TestLoadAndText(t *testing.T) {
	b := Load("abc\ndef")
	if b.Text() != "abc\ndef\n" {
		t.Fatalf("Text() = %q", b.Text())
	}
	if b.LineCount() != 2 || b.CurLine() != 0 || b.Col() != 0 {
		t.Fatalf("cursor state: %d %d %d", b.LineCount(), b.CurLine(), b.Col())
	}
}

func TestInsertBackspace(t *testing.T) {
	b := New()
	for _, r := range "hello" {
		b.Insert(r)
	}
	if b.Text() != "hello\n" || b.Col() != 5 {
		t.Fatalf("after insert: %q col=%d", b.Text(), b.Col())
	}
	b.Backspace()
	if b.Text() != "hell\n" || b.Col() != 4 {
		t.Fatalf("after backspace: %q col=%d", b.Text(), b.Col())
	}
}

func TestEnterSplitAndJoin(t *testing.T) {
	b := Load("abcd")
	b.MoveRight()
	b.MoveRight()
	b.InsertNewline()
	if b.Text() != "ab\ncd\n" || b.CurLine() != 1 || b.Col() != 0 {
		t.Fatalf("after split: %q line=%d col=%d", b.Text(), b.CurLine(), b.Col())
	}
	b.Backspace()
	if b.Text() != "abcd\n" || b.CurLine() != 0 || b.Col() != 2 {
		t.Fatalf("after join: %q line=%d col=%d", b.Text(), b.CurLine(), b.Col())
	}
}

func TestDeleteJoinsNextLine(t *testing.T) {
	b := Load("ab\ncd")
	b.LineEnd()
	b.Delete()
	if b.Text() != "abcd\n" {
		t.Fatalf("Text() = %q", b.Text())
	}
}

func TestBackspaceAtStartOfFirstLine(t *testing.T) {
	b := Load("abc")
	b.LineStart()
	b.Backspace()
	if b.Text() != "abc\n" || b.Col() != 0 {
		t.Fatalf("no-op backspace changed buffer: %q", b.Text())
	}
}

func TestUndoRedo(t *testing.T) {
	b := New()
	for _, r := range "ab" {
		b.Insert(r)
	}
	if b.Text() != "ab\n" {
		t.Fatalf("after typing: %q", b.Text())
	}
	b.InsertNewline()
	b.Insert('c')
	if b.Text() != "ab\nc\n" {
		t.Fatalf("after split+type: %q", b.Text())
	}
	b.Undo()
	if b.Text() != "ab\n\n" {
		t.Fatalf("after undo (drop 'c'): %q", b.Text())
	}
	b.Undo()
	if b.Text() != "ab\n" {
		t.Fatalf("after undo (drop newline): %q", b.Text())
	}
	b.Redo()
	if b.Text() != "ab\n\n" {
		t.Fatalf("after redo (restore line): %q", b.Text())
	}
	b.Redo()
	if b.Text() != "ab\nc\n" {
		t.Fatalf("after redo (restore 'c'): %q", b.Text())
	}
	b.Redo()
	if b.Text() != "ab\nc\n" {
		t.Fatalf("redo must be exhausted: %q", b.Text())
	}
}

func TestUndoRestoresCursor(t *testing.T) {
	b := Load("one")
	b.LineEnd()
	for _, r := range " two" {
		b.Insert(r)
	}
	b.Undo()
	if b.Text() != "one\n" || b.Col() != 3 {
		t.Fatalf("undo: %q col=%d", b.Text(), b.Col())
	}
}

func TestInsertRunIsOneUndoStep(t *testing.T) {
	b := Load("one")
	b.LineEnd()
	for _, r := range " two" {
		b.Insert(r)
	}
	b.Undo()
	if b.Text() != "one\n" || b.Col() != 3 {
		t.Fatalf("undo: %q col=%d", b.Text(), b.Col())
	}
}

func TestMoveBreaksInsertGroup(t *testing.T) {
	b := New()
	for _, r := range "ab" {
		b.Insert(r)
	}
	b.MoveLeft()
	b.Insert('X')
	if b.Text() != "aXb\n" {
		t.Fatalf("after insert: %q", b.Text())
	}
	b.Undo()
	if b.Text() != "ab\n" {
		t.Fatalf("undo: %q", b.Text())
	}
}

func TestMoveUpDownClampWithGoalCol(t *testing.T) {
	b := Load("abcdef\nx")
	b.LineEnd()
	b.MoveDown()
	if b.CurLine() != 1 || b.Col() != 1 {
		t.Fatalf("down: line=%d col=%d", b.CurLine(), b.Col())
	}
	b.MoveUp()
	if b.Col() != 6 {
		t.Fatalf("up restored col=%d, want 6", b.Col())
	}
}

func TestDirtyMarkSaved(t *testing.T) {
	b := Load("hi")
	if b.Dirty() {
		t.Fatal("loaded buffer must be clean")
	}
	b.Insert('!')
	if !b.Dirty() {
		t.Fatal("edited buffer must be dirty")
	}
	b.MarkSaved()
	if b.Dirty() {
		t.Fatal("saved buffer must be clean")
	}
}

func TestSetCursor(t *testing.T) {
	b := Load("hello\nworld")
	b.SetCursor(1, 3)
	if b.CurLine() != 1 || b.Col() != 3 {
		t.Fatalf("SetCursor(1, 3): line=%d col=%d", b.CurLine(), b.Col())
	}
	b.SetCursor(-5, -2)
	if b.CurLine() != 0 || b.Col() != 0 {
		t.Fatalf("SetCursor(-5, -2): line=%d col=%d", b.CurLine(), b.Col())
	}
	b.SetCursor(100, 100)
	if b.CurLine() != 1 || b.Col() != 5 {
		t.Fatalf("SetCursor(100, 100): line=%d col=%d", b.CurLine(), b.Col())
	}
}

func TestReplaceRange(t *testing.T) {
	b := Load("hello world\nfoo bar")
	ok := b.ReplaceRange(0, 6, 5, []rune("dmed"))
	if !ok || b.Text() != "hello dmed\nfoo bar\n" {
		t.Fatalf("ReplaceRange: ok=%v text=%q", ok, b.Text())
	}
	if b.CurLine() != 0 || b.Col() != 10 {
		t.Fatalf("after replace: line=%d col=%d", b.CurLine(), b.Col())
	}
	b.Undo()
	if b.Text() != "hello world\nfoo bar\n" {
		t.Fatalf("after undo: %q", b.Text())
	}
	b.Redo()
	if b.Text() != "hello dmed\nfoo bar\n" {
		t.Fatalf("after redo: %q", b.Text())
	}
}

func TestReplaceAll(t *testing.T) {
	b := Load("banana apple banana\nbanana split")
	count := b.ReplaceAll("banana", "orange")
	if count != 3 || b.Text() != "orange apple orange\norange split\n" {
		t.Fatalf("ReplaceAll: count=%d text=%q", count, b.Text())
	}
	b.Undo()
	if b.Text() != "banana apple banana\nbanana split\n" {
		t.Fatalf("after undo: %q", b.Text())
	}
	b.Redo()
	if b.Text() != "orange apple orange\norange split\n" {
		t.Fatalf("after redo: %q", b.Text())
	}
}

func TestSelectionAndInsert(t *testing.T) {
	b := Load("hello beautiful world\n")
	b.SetCursor(0, 6)
	for i := 0; i < 9; i++ {
		b.MoveRightWithSelect()
	}
	if !b.HasSelection() {
		t.Fatal("expected active selection")
	}
	if b.SelectedText() != "beautiful" {
		t.Fatalf("expected 'beautiful', got %q", b.SelectedText())
	}

	// Insert replaces selection
	b.InsertText("brave new")
	if b.Text() != "hello brave new world\n" {
		t.Fatalf("after insert: %q", b.Text())
	}
	if b.HasSelection() {
		t.Fatal("selection should be cleared after insert")
	}

	b.Undo()
	if b.Text() != "hello beautiful world\n" {
		t.Fatalf("after undo: %q", b.Text())
	}
}

func TestDeleteLineRemovesCurrentLine(t *testing.T) {
	b := Load("line1\nline2\nline3\n")
	b.DeleteLine()
	if b.Text() != "line2\nline3\n" || b.CurLine() != 0 || b.Col() != 0 {
		t.Fatalf("delete line: %q line=%d col=%d", b.Text(), b.CurLine(), b.Col())
	}
}

func TestDeleteLineLastLine(t *testing.T) {
	b := Load("line1\nline2\n")
	b.MoveDown()
	b.DeleteLine()
	if b.Text() != "line1\n" || b.CurLine() != 0 || b.Col() != 0 {
		t.Fatalf("delete last line: %q line=%d col=%d", b.Text(), b.CurLine(), b.Col())
	}
}

func TestDeleteLineSingleLine(t *testing.T) {
	b := Load("only")
	b.DeleteLine()
	if b.Text() != "\n" || b.CurLine() != 0 || b.Col() != 0 {
		t.Fatalf("delete single line: %q line=%d col=%d", b.Text(), b.CurLine(), b.Col())
	}
}

func TestDeleteLineUndo(t *testing.T) {
	b := Load("a\nb\nc\n")
	b.DeleteLine()
	if b.Text() != "b\nc\n" || b.CurLine() != 0 || b.Col() != 0 {
		t.Fatalf("after delete: %q line=%d col=%d", b.Text(), b.CurLine(), b.Col())
	}
	b.Undo()
	if b.Text() != "a\nb\nc\n" || b.CurLine() != 0 || b.Col() != 0 {
		t.Fatalf("after undo: %q line=%d col=%d", b.Text(), b.CurLine(), b.Col())
	}
}

func TestSelectionDeleteUndo(t *testing.T) {
	b := Load("line1\nline2\nline3\n")
	b.SetCursor(0, 5)
	b.MoveDownWithSelect()
	b.LineEndWithSelect()

	if b.SelectedText() != "\nline2" {
		t.Fatalf("selected text: %q", b.SelectedText())
	}

	b.DeleteSelection()
	if b.Text() != "line1\nline3\n" {
		t.Fatalf("after delete selection: %q", b.Text())
	}

	b.Undo()
	if b.Text() != "line1\nline2\nline3\n" {
		t.Fatalf("after undo: %q", b.Text())
	}
}

func TestMoveLineDown(t *testing.T) {
	b := Load("aaa\nbbb\nccc\n")
	b.SetCursor(0, 0)
	b.MoveLineDown()
	if b.Text() != "bbb\naaa\nccc\n" {
		t.Fatalf("MoveLineDown: %q", b.Text())
	}
	if b.CurLine() != 1 {
		t.Fatalf("cursor after move down: line=%d", b.CurLine())
	}
}

func TestMoveLineUp(t *testing.T) {
	b := Load("aaa\nbbb\nccc\n")
	b.SetCursor(1, 0)
	b.MoveLineUp()
	if b.Text() != "bbb\naaa\nccc\n" {
		t.Fatalf("MoveLineUp: %q", b.Text())
	}
	if b.CurLine() != 0 {
		t.Fatalf("cursor after move up: line=%d", b.CurLine())
	}
}

func TestMoveLineUpFirstLine(t *testing.T) {
	b := Load("aaa\nbbb\n")
	b.SetCursor(0, 0)
	b.MoveLineUp() // no-op
	if b.Text() != "aaa\nbbb\n" {
		t.Fatalf("MoveLineUp first line: %q", b.Text())
	}
}

func TestMoveLineDownLastLine(t *testing.T) {
	b := Load("aaa\nbbb\n")
	b.SetCursor(1, 0)
	b.MoveLineDown() // no-op
	if b.Text() != "aaa\nbbb\n" {
		t.Fatalf("MoveLineDown last line: %q", b.Text())
	}
}

func TestMoveLineSelection(t *testing.T) {
	b := Load("aaa\nbbb\nccc\nddd\n")
	b.SetCursor(1, 0)
	b.StartSelection()
	b.MoveDownWithSelect() // select lines 1-2
	b.MoveLineDown()
	// selection moves: lines 1-2 (bbb,ccc) swap with ddd → aaa,ddd,bbb,ccc
	if b.Text() != "aaa\nddd\nbbb\nccc\n" {
		t.Fatalf("MoveLineDown selection: %q", b.Text())
	}
}

func TestMoveLineUndo(t *testing.T) {
	b := Load("aaa\nbbb\nccc\n")
	b.SetCursor(0, 0)
	b.MoveLineDown()
	if b.Text() != "bbb\naaa\nccc\n" {
		t.Fatalf("after move: %q", b.Text())
	}
	b.Undo()
	if b.Text() != "aaa\nbbb\nccc\n" {
		t.Fatalf("after undo: %q", b.Text())
	}
}

func TestDuplicateLine(t *testing.T) {
	b := Load("aaa\nbbb\nccc\n")
	b.SetCursor(1, 0)
	b.DuplicateLine()
	if b.Text() != "aaa\nbbb\nbbb\nccc\n" {
		t.Fatalf("DuplicateLine: %q", b.Text())
	}
	if b.CurLine() != 2 {
		t.Fatalf("cursor after dup: line=%d", b.CurLine())
	}
}

func TestDuplicateFirstLine(t *testing.T) {
	b := Load("aaa\nbbb\n")
	b.SetCursor(0, 0)
	b.DuplicateLine()
	if b.Text() != "aaa\naaa\nbbb\n" {
		t.Fatalf("DuplicateFirstLine: %q", b.Text())
	}
}

func TestDuplicateSelection(t *testing.T) {
	b := Load("aaa\nbbb\nccc\n")
	b.SetCursor(0, 0)
	b.StartSelection()
	b.MoveDownWithSelect() // select lines 0-1
	b.DuplicateLine()
	if b.Text() != "aaa\nbbb\naaa\nbbb\nccc\n" {
		t.Fatalf("DuplicateSelection: %q", b.Text())
	}
}

func TestDuplicateLineUndo(t *testing.T) {
	b := Load("aaa\nbbb\n")
	b.SetCursor(0, 0)
	b.DuplicateLine()
	if b.Text() != "aaa\naaa\nbbb\n" {
		t.Fatalf("after dup: %q", b.Text())
	}
	b.Undo()
	if b.Text() != "aaa\nbbb\n" {
		t.Fatalf("after undo: %q", b.Text())
	}
}

func TestDuplicateLineUp(t *testing.T) {
	b := Load("aaa\nbbb\nccc\n")
	b.SetCursor(1, 0)
	b.DuplicateLineUp()
	if b.Text() != "aaa\nbbb\nbbb\nccc\n" {
		t.Fatalf("DuplicateLineUp: %q", b.Text())
	}
	if b.CurLine() != 2 {
		t.Fatalf("cursor after dup up: line=%d", b.CurLine())
	}
}

func TestDuplicateLineUpSelection(t *testing.T) {
	b := Load("aaa\nbbb\nccc\nddd\n")
	b.SetCursor(1, 0)
	b.StartSelection()
	b.MoveDownWithSelect() // select lines 1-2
	b.DuplicateLineUp()
	if b.Text() != "aaa\nbbb\nccc\nbbb\nccc\nddd\n" {
		t.Fatalf("DuplicateLineUpSelection: %q", b.Text())
	}
	if b.CurLine() != 3 {
		t.Fatalf("cursor after dup up sel: line=%d", b.CurLine())
	}
}

func TestToggleCommentSingleLine(t *testing.T) {
	b := Load("hello world")
	b.SetCursor(0, 5)
	b.ToggleComment("//", "")
	want := "// hello world\n"
	if b.Text() != want {
		t.Fatalf("comment: got %q want %q", b.Text(), want)
	}
	if b.Col() != 8 {
		t.Fatalf("col after comment: got %d want 8", b.Col())
	}
	b.ToggleComment("//", "")
	if b.Text() != "hello world\n" {
		t.Fatalf("uncomment: got %q", b.Text())
	}
	if b.Col() != 5 {
		t.Fatalf("col after uncomment: got %d want 5", b.Col())
	}
}

func TestToggleCommentIndented(t *testing.T) {
	b := Load("    code()")
	b.SetCursor(0, 4)
	b.ToggleComment("//", "")
	want := "    // code()\n"
	if b.Text() != want {
		t.Fatalf("indented comment: got %q want %q", b.Text(), want)
	}
	if b.Col() != 7 {
		t.Fatalf("col: got %d want 7", b.Col())
	}
	b.ToggleComment("//", "")
	if b.Text() != "    code()\n" {
		t.Fatalf("uncomment indented: got %q", b.Text())
	}
	if b.Col() != 4 {
		t.Fatalf("col: got %d want 4", b.Col())
	}
}

func TestToggleCommentSelection(t *testing.T) {
	b := Load("aaa\nbbb\nccc")
	b.SetCursor(0, 0)
	b.StartSelection()
	b.MoveDownWithSelect() // select lines 0-1
	b.ToggleComment("//", "")
	want := "// aaa\n// bbb\nccc\n"
	if b.Text() != want {
		t.Fatalf("comment selection: got %q want %q", b.Text(), want)
	}
}

func TestToggleCommentBlock(t *testing.T) {
	b := Load("text")
	b.SetCursor(0, 0)
	b.ToggleComment("<!--", "-->")
	want := "<!-- text -->\n"
	if b.Text() != want {
		t.Fatalf("block comment: got %q want %q", b.Text(), want)
	}
	b.ToggleComment("<!--", "-->")
	if b.Text() != "text\n" {
		t.Fatalf("block uncomment: got %q", b.Text())
	}
}

func TestToggleCommentNoop(t *testing.T) {
	b := Load("foo")
	b.SetCursor(0, 0)
	b.ToggleComment("", "")
	if b.Text() != "foo\n" {
		t.Fatalf("noop comment changed text: %q", b.Text())
	}
}

func TestToggleCommentColumns(t *testing.T) {
	b := Load("aaa\nbbb\nccc")
	b.SetCursor(2, 2)
	b.ToggleComment("//", "")
	if b.Col() != 5 {
		t.Fatalf("col on line 2 after comment: got %d want 5", b.Col())
	}
	// verify content of line 2
	b.ToggleComment("//", "")
	if b.Col() != 2 {
		t.Fatalf("col on line 2 after uncomment: got %d want 2", b.Col())
	}
}

func TestInsertOverSelectionKeepsTypedChar(t *testing.T) {
	b := Load("hello beautiful world\n")
	b.SetCursor(0, 6)
	for i := 0; i < 9; i++ {
		b.MoveRightWithSelect()
	}
	b.Insert('x')
	if b.Text() != "hello x world\n" {
		t.Fatalf("typed over selection: %q", b.Text())
	}
	if b.HasSelection() {
		t.Fatal("selection must be gone")
	}
	b.Undo()
	if b.Text() != "hello beautiful world\n" {
		t.Fatalf("after undo: %q", b.Text())
	}
}

func TestInsertNewlineOverSelection(t *testing.T) {
	b := Load("hello beautiful world\n")
	b.SetCursor(0, 6)
	for i := 0; i < 9; i++ {
		b.MoveRightWithSelect()
	}
	b.InsertNewline()
	if b.Text() != "hello \n world\n" {
		t.Fatalf("Enter over selection: %q", b.Text())
	}
}

func TestIndentLinesSelection(t *testing.T) {
	b := Load("a\nbb\nccc\n")
	b.SetCursor(0, 1)
	b.MoveDownWithSelect()
	b.IndentLines("\t")
	if b.Text() != "\ta\n\tbb\nccc\n" {
		t.Fatalf("indent block: %q", b.Text())
	}
	// The caret rides at the end of the range and the block stays selected.
	if b.CurLine() != 1 || b.Col() != 2 {
		t.Fatalf("caret after indent: line=%d col=%d", b.CurLine(), b.Col())
	}
	if !b.HasSelection() {
		t.Fatal("block Tab keeps the selection")
	}
	b.Undo()
	if b.Text() != "a\nbb\nccc\n" {
		t.Fatalf("after undo: %q", b.Text())
	}
}

func TestIndentLinesNoSelection(t *testing.T) {
	b := Load("a\nbb\n")
	b.SetCursor(1, 2)
	b.IndentLines("\t")
	if b.Text() != "a\n\tbb\n" {
		t.Fatalf("indent current line: %q", b.Text())
	}
	if b.CurLine() != 1 || b.Col() != 3 || b.HasSelection() {
		t.Fatalf("caret after indent: line=%d col=%d sel=%v", b.CurLine(), b.Col(), b.HasSelection())
	}
}

func TestUnindentLinesRemovesOneLevel(t *testing.T) {
	b := Load("\ta\n b\n    c\n")
	b.SetCursor(0, 2)
	b.MoveDownWithSelect()
	b.MoveDownWithSelect()
	b.UnindentLines("\t")
	if b.Text() != "a\nb\n   c\n" {
		t.Fatalf("unindent: %q", b.Text())
	}
}

func TestIndentedNewlineSingleUndo(t *testing.T) {
	b := Load("\tbody\n")
	b.SetCursor(0, 6) // end of "\tbody"
	b.IndentedNewline("\t\t")
	if b.Text() != "\tbody\n\t\t\n" || b.CurLine() != 1 || b.Col() != 2 {
		t.Fatalf("indented newline: %q line=%d col=%d", b.Text(), b.CurLine(), b.Col())
	}
	b.Undo()
	if b.Text() != "\tbody\n" {
		t.Fatalf("after undo: %q", b.Text())
	}
}

func TestIndentedNewlineOverSelection(t *testing.T) {
	b := Load("hello beautiful world\n")
	b.SetCursor(0, 6)
	for i := 0; i < 9; i++ {
		b.MoveRightWithSelect()
	}
	b.IndentedNewline("\t")
	if b.Text() != "hello \n\t world\n" {
		t.Fatalf("Enter+indent over selection: %q", b.Text())
	}
}

func TestReplaceSpan(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		sl, sc      int
		el, ec      int
		replacement string
		want        string
		wantLine    int
		wantCol     int
	}{
		{
			"single-line replace",
			"hello world\nsecond\n", 0, 6, 0, 11, "there",
			"hello there\nsecond\n", 0, 11,
		},
		{
			"multi-line replace",
			"one two\nthree four\nfive\n", 0, 3, 1, 5, "A\nB\nC",
			"oneA\nB\nC four\nfive\n", 2, 1,
		},
		{
			"delete multiline range",
			"aa\nbb\ncc\n", 0, 2, 1, 2, "",
			"aa\ncc\n", 0, 2,
		},
		{
			"whole-line replace",
			"x\ny\nz\n", 1, 0, 1, 1, "why\n",
			"x\nwhy\n\nz\n", 2, 0,
		},
	}
	for _, tc := range cases {
		b := Load(tc.in)
		if !b.ReplaceSpan(tc.sl, tc.sc, tc.el, tc.ec, tc.replacement) {
			t.Fatalf("%s: ReplaceSpan = false", tc.name)
		}
		if b.Text() != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, b.Text(), tc.want)
		}
		if b.CurLine() != tc.wantLine || b.Col() != tc.wantCol {
			t.Errorf("%s: cursor %d:%d, want %d:%d", tc.name, b.CurLine(), b.Col(), tc.wantLine, tc.wantCol)
		}
		// Exactly one undo step restores the original.
		b.Undo()
		if b.Text() != tc.in {
			t.Errorf("%s: after undo %q, want %q", tc.name, b.Text(), tc.in)
		}
	}
}

// ReplaceSpan rejects out-of-range positions.
func TestReplaceSpanRejectsOutOfRange(t *testing.T) {
	b := Load("one\ntwo\n")
	if b.ReplaceSpan(-1, 0, 0, 3, "x") {
		t.Fatal("negative start line accepted")
	}
	if b.ReplaceSpan(1, 0, 0, 3, "x") {
		t.Fatal("end before start accepted")
	}
	if b.ReplaceSpan(0, 0, 5, 3, "x") {
		t.Fatal("end past the document accepted")
	}
	if b.ReplaceSpan(0, 0, 0, 9, "x") {
		t.Fatal("end past the line accepted")
	}
}

// pushUndo is the choke point every mutating method goes through, so one table has
// to prove the last-edit tracker answers for all of them. The position it records
// is the caret the undo snapshot takes — where the change started, which is also
// what one Ctrl+Z restores. A method that edits away from the caret (ReplaceSpan
// for an LSP edit, a block transform) therefore records where the user was
// standing, not where the text moved.
func TestLastEditTracksEveryEditShape(t *testing.T) {
	cases := []struct {
		name              string
		in                string
		line, col         int
		edit              func(b *Buffer)
		wantLine, wantCol int
	}{
		{"a typing run is one step from its start",
			"one\ntwo\nthree\n", 1, 1,
			func(b *Buffer) { b.Insert('X'); b.Insert('Y') }, 1, 1},
		{"a caret move starts a new step at the new position",
			"one\ntwo\nthree\n", 1, 1,
			func(b *Buffer) { b.Insert('X'); b.SetCursor(1, 3); b.Insert('Y') }, 1, 3},
		{"InsertText", "one\ntwo\nthree\n", 1, 1,
			func(b *Buffer) { b.InsertText("A\nB") }, 1, 1},
		{"InsertNewline", "one\ntwo\nthree\n", 1, 1,
			func(b *Buffer) { b.InsertNewline() }, 1, 1},
		{"IndentedNewline", "one\ntwo\nthree\n", 1, 1,
			func(b *Buffer) { b.IndentedNewline("\t") }, 1, 1},
		{"Backspace", "one\ntwo\nthree\n", 1, 1,
			func(b *Buffer) { b.Backspace() }, 1, 1},
		{"Delete", "one\ntwo\nthree\n", 1, 1,
			func(b *Buffer) { b.Delete() }, 1, 1},
		{"DeleteSelection", "one\ntwo\nthree\n", 0, 0,
			func(b *Buffer) { b.SelectRange(1, 0, 1, 3); b.DeleteSelection() }, 1, 3},
		{"DeleteLine", "one\ntwo\nthree\n", 1, 1,
			func(b *Buffer) { b.DeleteLine() }, 1, 1},
		{"DuplicateLine", "one\ntwo\nthree\n", 1, 2,
			func(b *Buffer) { b.DuplicateLine() }, 1, 2},
		{"ReplaceSpan keeps the caret rather than the span",
			"one\ntwo\nthree\n", 2, 2,
			func(b *Buffer) { b.ReplaceSpan(0, 0, 0, 3, "ONE") }, 2, 2},
		{"ReplaceRange", "one\ntwo\nthree\n", 1, 0,
			func(b *Buffer) { b.ReplaceRange(0, 0, 3, []rune("ONE")) }, 1, 0},
		{"ReplaceAll", "one\ntwo\nthree\n", 1, 1,
			func(b *Buffer) { b.ReplaceAll("o", "0") }, 1, 1},
		{"MoveLineUp", "one\ntwo\nthree\n", 2, 1,
			func(b *Buffer) { b.MoveLineUp() }, 2, 1},
		{"MoveLineDown", "one\ntwo\nthree\n", 0, 1,
			func(b *Buffer) { b.MoveLineDown() }, 0, 1},
		{"IndentLines", "one\ntwo\nthree\n", 1, 2,
			func(b *Buffer) { b.IndentLines("\t") }, 1, 2},
		{"UnindentLines", "\tone\ntwo\n", 0, 3,
			func(b *Buffer) { b.UnindentLines("\t") }, 0, 3},
		{"ToggleComment", "one\ntwo\nthree\n", 1, 1,
			func(b *Buffer) { b.ToggleComment("//", "") }, 1, 1},
		{"SortLines", "three\none\ntwo\n", 2, 1,
			func(b *Buffer) { b.SortLines(0, 2) }, 2, 1},
		{"ApplyCase", "one\ntwo\n", 1, 2,
			func(b *Buffer) { b.ApplyCase(true) }, 1, 2},
		{"TrimTrailing", "one  \ntwo\n", 0, 1,
			func(b *Buffer) { b.TrimTrailing(0, 1) }, 0, 1},
		{"NumberLines", "one\ntwo\n", 1, 0,
			func(b *Buffer) { b.NumberLines(0, 1) }, 1, 0},
		{"a multi-cursor insert", "one\ntwo\nthree\n", 1, 1,
			func(b *Buffer) { b.AddCursor(2, 1, 1, 1); b.MultiInsertRune('X') }, 1, 1},
		{"a multi-cursor newline", "one\ntwo\nthree\n", 1, 1,
			func(b *Buffer) { b.AddCursor(2, 1, 1, 1); b.MultiNewline() }, 1, 1},
	}
	for _, tc := range cases {
		b := Load(tc.in)
		b.SetCursor(tc.line, tc.col)
		tc.edit(b)
		if b.Text() == tc.in {
			t.Fatalf("%s: the edit changed nothing, so it proves nothing", tc.name)
		}
		line, col, ok := b.LastEdit()
		if !ok {
			t.Fatalf("%s: LastEdit must be set by any edit", tc.name)
		}
		if line != tc.wantLine || col != tc.wantCol {
			t.Errorf("%s: LastEdit = %d:%d, want %d:%d", tc.name, line, col, tc.wantLine, tc.wantCol)
		}
	}
}

// Undoing the change does not clear the tracker: after a Ctrl+Z the place the user
// was working is still the answer they want.
func TestLastEditSurvivesUndo(t *testing.T) {
	b := Load("one\ntwo\nthree\n")
	b.SetCursor(1, 1)
	b.Insert('X')
	b.SetCursor(2, 0)
	if !b.Undo() {
		t.Fatal("Undo = false")
	}
	line, col, ok := b.LastEdit()
	if !ok || line != 1 || col != 1 {
		t.Fatalf("LastEdit after undo = %d:%d ok=%v, want 1:1", line, col, ok)
	}
}

func TestLastEditUnsetOnLoad(t *testing.T) {
	for name, b := range map[string]*Buffer{"New": New(), "Load": Load("one\ntwo\n")} {
		if _, _, ok := b.LastEdit(); ok {
			t.Errorf("%s: an untouched buffer must not report a last edit", name)
		}
	}
}

// Only a change of text moves the tracker; wandering around the file must not
// masquerade as an edit, or "go to last change" answers with the caret itself.
func TestLastEditNotRecordedByCursorMoves(t *testing.T) {
	b := Load("one\ntwo\nthree\n")
	b.SetCursor(1, 2)
	b.MoveLeft()
	b.MoveRight()
	b.MoveUp()
	b.MoveDown()
	b.LineStart()
	b.LineEnd()
	b.StartSelection()
	b.DragSelect(2, 1)
	b.ClearSelection()
	b.SelectRange(0, 1, 1, 1)
	b.Deselect()
	b.AddCursor(2, 1, 1, 1)
	b.MoveAllLeft()
	b.MarkSaved()
	if _, _, ok := b.LastEdit(); ok {
		t.Fatal("cursor and selection moves must not record a last edit")
	}

	b.SetCursor(1, 1)
	b.Insert('X')
	b.SetCursor(2, 2)
	b.ClearCursors()
	b.MoveUp()
	line, col, ok := b.LastEdit()
	if !ok || line != 1 || col != 1 {
		t.Fatalf("LastEdit = %d:%d ok=%v, want the edit at 1:1 to stand", line, col, ok)
	}
}
