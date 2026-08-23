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
