package buffer

import "testing"

func multi(b *Buffer) []Cursor { return b.Cursors() }

func TestMultiInsertOnLines(t *testing.T) {
	b := Load("foo\nfoo\nfoo")
	b.SetCursor(0, 0)
	b.AddCursor(1, 0, 0, 0)
	b.AddCursor(2, 0, 0, 0)
	b.MultiInsertText("X")
	if b.Text() != "Xfoo\nXfoo\nXfoo\n" {
		t.Fatalf("got %q", b.Text())
	}
	c := multi(b)
	if len(c) != 3 {
		t.Fatalf("cursors=%d", len(c))
	}
	for _, c := range c {
		if c.Col != 1 {
			t.Fatalf("cursor col=%d", c.Col)
		}
	}
}

func TestMultiInsertSameLine(t *testing.T) {
	b := Load("abcd")
	b.SetCursor(0, 1)
	b.AddCursor(0, 3, 3, 3)
	b.MultiInsertText("_")
	if b.Text() != "a_bc_d\n" {
		t.Fatalf("got %q", b.Text())
	}
}

func TestMultiReplaceSelections(t *testing.T) {
	b := Load("one two one two")
	b.SetCursor(0, 4)
	b.AddCursor(0, 12, 12, 15) // select "two" at col12..15
	// main cursor replaces its word too
	wf, wt := b.WordAt(0, 4)
	b.col = wt
	b.selLine, b.selCol = 0, wf
	b.hasSelection = true
	b.MultiInsertText("x")
	if b.Text() != "one x one x\n" {
		t.Fatalf("got %q", b.Text())
	}
}

func TestMultiNewline(t *testing.T) {
	b := Load("ab\ncd\nef")
	b.SetCursor(0, 1)
	b.AddCursor(1, 1, 1, 1)
	b.MultiNewline()
	if b.Text() != "a\nb\nc\nd\nef\n" {
		t.Fatalf("got %q", b.Text())
	}
}

func TestMultiBackspace(t *testing.T) {
	b := Load("abc\ndef\nghi")
	b.SetCursor(0, 3)
	b.AddCursor(1, 3, 3, 3)
	b.AddCursor(2, 3, 3, 3)
	b.MultiBackspace()
	if b.Text() != "ab\nde\ngh\n" {
		t.Fatalf("got %q", b.Text())
	}
	c := multi(b)
	for _, c := range c {
		if c.Col != 2 {
			t.Fatalf("cursor col=%d", c.Col)
		}
	}
}

func TestMultiDelete(t *testing.T) {
	b := Load("abc\ndef\nghi")
	b.SetCursor(0, 0)
	b.AddCursor(1, 0, 0, 0)
	b.AddCursor(2, 0, 0, 0)
	b.MultiDelete()
	if b.Text() != "bc\nef\nhi\n" {
		t.Fatalf("got %q", b.Text())
	}
}

func TestMultiUndo(t *testing.T) {
	b := Load("aaa\nbbb")
	b.SetCursor(0, 0)
	b.AddCursor(1, 0, 1, 0)
	b.MultiInsertText("X")
	if b.Text() != "Xaaa\nXbbb\n" {
		t.Fatalf("insert got %q", b.Text())
	}
	if !b.Undo() {
		t.Fatal("undo returned false")
	}
	if b.Text() != "aaa\nbbb\n" {
		t.Fatalf("undo got %q", b.Text())
	}
}

func TestAddNextOccurrence(t *testing.T) {
	b := Load("cat dog cat bird cat")
	b.SetCursor(0, 0)
	// Select first "cat" and arm multi mode.
	if !b.AddNextOccurrence() {
		t.Fatal("first call should arm multi mode")
	}
	if !b.HasSelection() {
		t.Fatal("expected the word under main cursor selected")
	}
	if b.word == "" {
		t.Fatal("expected multi mode armed (word stored)")
	}
	// Next occurrence: the second "cat".
	if !b.AddNextOccurrence() {
		t.Fatal("second call should add a cursor")
	}
	if len(multi(b)) != 2 {
		t.Fatalf("expected 2 cursors, got %d", len(multi(b)))
	}
	// Third "cat".
	if !b.AddNextOccurrence() {
		t.Fatal("third call should add a cursor")
	}
	if len(multi(b)) != 3 {
		t.Fatalf("expected 3 cursors, got %d", len(multi(b)))
	}
	// Wrap-around: no more new cursor.
	if b.AddNextOccurrence() {
		t.Fatal("should not add beyond wrap")
	}
}

func TestClearCursors(t *testing.T) {
	b := Load("x")
	b.SetCursor(0, 0)
	b.AddCursor(0, 1, 0, 1)
	if !b.HasMultipleCursors() {
		t.Fatal("expected multiple cursors")
	}
	b.ClearCursors()
	if b.HasMultipleCursors() {
		t.Fatal("expected cursors cleared")
	}
}

func TestMoveAllDown(t *testing.T) {
	b := Load("aa\nbb\ncc")
	b.SetCursor(0, 1)
	b.AddCursor(1, 1, 1, 1)
	b.MoveAllDown()
	c := multi(b)
	if c[0].Line != 1 || c[1].Line != 2 {
		t.Fatalf("cursors after down: %+v", c)
	}
}
