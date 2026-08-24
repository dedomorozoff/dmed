package vcs

import "testing"

func TestSideBySideRows(t *testing.T) {
	head := "a\nb\nc\nd\n"
	buf := "a\nB\nc\nd\ne\n"
	rows := SideBySide(head, buf)

	want := []DiffRow{
		{0, 0, DiffNone},
		{1, 1, DiffModified}, // b → B zipped side by side
		{2, 2, DiffNone},
		{3, 3, DiffNone},
		{-1, 4, DiffAdded}, // trailing insert has no left counterpart
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(rows), len(want), rows)
	}
	for i, r := range want {
		if rows[i] != r {
			t.Fatalf("row %d = %+v, want %+v", i, rows[i], r)
		}
	}
}

func TestSideBySideDeletion(t *testing.T) {
	head := "a\nb\nc\n"
	buf := "a\nc\n"
	rows := SideBySide(head, buf)

	want := []DiffRow{
		{0, 0, DiffNone},
		{1, -1, DiffDeleted}, // b exists only in HEAD
		{2, 1, DiffNone},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(rows), len(want), rows)
	}
	for i, r := range want {
		if rows[i] != r {
			t.Fatalf("row %d = %+v, want %+v", i, rows[i], r)
		}
	}
}

func TestSideBySideInsertAtStart(t *testing.T) {
	head := "x\n"
	buf := "new1\nnew2\nx\ny\n"
	rows := SideBySide(head, buf)

	want := []DiffRow{
		{-1, 0, DiffAdded},
		{-1, 1, DiffAdded},
		{0, 2, DiffNone},
		{-1, 3, DiffAdded},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(rows), len(want), rows)
	}
	for i, r := range want {
		if rows[i] != r {
			t.Fatalf("row %d = %+v, want %+v", i, rows[i], r)
		}
	}
}
