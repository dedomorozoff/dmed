package buffer

import (
	"testing"
)

func TestMatchBracketSameLine(t *testing.T) {
	b := Load("func foo(a, b int) { return }\n")

	// Cursor directly on '(' (col 8)
	m, ok := b.MatchDelimiter(0, 8)
	if !ok {
		t.Fatal("expected match for '('")
	}
	if m.FromCol != 8 || m.ToCol != 17 {
		t.Fatalf("expected 8 -> 17, got %d -> %d", m.FromCol, m.ToCol)
	}

	// Cursor directly on ')' (col 17)
	m, ok = b.MatchDelimiter(0, 17)
	if !ok {
		t.Fatal("expected match for ')'")
	}
	if m.FromCol != 17 || m.ToCol != 8 {
		t.Fatalf("expected 17 -> 8, got %d -> %d", m.FromCol, m.ToCol)
	}

	// Cursor immediately after ')' (col 18)
	m, ok = b.MatchDelimiter(0, 18)
	if !ok {
		t.Fatal("expected match for ')' when cursor is at col 18")
	}
	if m.FromCol != 17 || m.ToCol != 8 {
		t.Fatalf("expected 17 -> 8, got %d -> %d", m.FromCol, m.ToCol)
	}

	// Cursor on '{' (col 19)
	m, ok = b.MatchDelimiter(0, 19)
	if !ok {
		t.Fatal("expected match for '{'")
	}
	if m.FromCol != 19 || m.ToCol != 28 {
		t.Fatalf("expected 19 -> 28, got %d -> %d", m.FromCol, m.ToCol)
	}
}

func TestMatchBracketMultiline(t *testing.T) {
	code := `func test() {
	for i := 0; i < 10; i++ {
		doSomething()
	}
}`
	b := Load(code)

	// Opening '{' of func on line 0, col 12
	m, ok := b.MatchDelimiter(0, 12)
	if !ok {
		t.Fatal("expected match for func '{'")
	}
	if m.ToLine != 4 || m.ToCol != 0 {
		t.Fatalf("expected match at (4, 0), got (%d, %d)", m.ToLine, m.ToCol)
	}

	// Closing '}' of func on line 4, col 0
	m, ok = b.MatchDelimiter(4, 0)
	if !ok {
		t.Fatal("expected match for func '}'")
	}
	if m.ToLine != 0 || m.ToCol != 12 {
		t.Fatalf("expected match at (0, 12), got (%d, %d)", m.ToLine, m.ToCol)
	}

	// Closing '}' of for loop on line 3, col 1
	m, ok = b.MatchDelimiter(3, 1)
	if !ok {
		t.Fatal("expected match for for '}'")
	}
	if m.ToLine != 1 || m.ToCol != 25 {
		t.Fatalf("expected match at (1, 25), got (%d, %d)", m.ToLine, m.ToCol)
	}
}

func TestMatchSquareBrackets(t *testing.T) {
	b := Load("arr[i][j]\n")

	// '[' at col 3 matches ']' at col 5
	m, ok := b.MatchDelimiter(0, 3)
	if !ok || m.ToCol != 5 {
		t.Fatalf("expected match 3 -> 5, got %+v, ok=%v", m, ok)
	}

	// '[' at col 6 matches ']' at col 8
	m, ok = b.MatchDelimiter(0, 6)
	if !ok || m.ToCol != 8 {
		t.Fatalf("expected match 6 -> 8, got %+v, ok=%v", m, ok)
	}
}

func TestMatchQuotesSameLine(t *testing.T) {
	b := Load(`s := "hello" + "world"` + "\n")

	// Opening quote col 5
	m, ok := b.MatchDelimiter(0, 5)
	if !ok || m.ToCol != 11 {
		t.Fatalf("expected 5 -> 11, got %+v, ok=%v", m, ok)
	}

	// Closing quote col 11
	m, ok = b.MatchDelimiter(0, 11)
	if !ok || m.ToCol != 5 {
		t.Fatalf("expected 11 -> 5, got %+v, ok=%v", m, ok)
	}

	// Second string: opening col 15 -> 21
	m, ok = b.MatchDelimiter(0, 15)
	if !ok || m.ToCol != 21 {
		t.Fatalf("expected 15 -> 21, got %+v, ok=%v", m, ok)
	}
}

func TestMatchSingleQuotesWithApostrophe(t *testing.T) {
	b := Load(`val := 'x'; // don't break` + "\n")

	m, ok := b.MatchDelimiter(0, 7)
	if !ok || m.ToCol != 9 {
		t.Fatalf("expected 7 -> 9, got %+v, ok=%v", m, ok)
	}
}

func TestMatchBacktickMultiline(t *testing.T) {
	code := "raw := `first line\nsecond line`\n"
	b := Load(code)

	m, ok := b.MatchDelimiter(0, 7)
	if !ok {
		t.Fatal("expected match for backtick")
	}
	if m.ToLine != 1 || m.ToCol != 11 {
		t.Fatalf("expected match at (1, 11), got (%d, %d)", m.ToLine, m.ToCol)
	}

	m, ok = b.MatchDelimiter(1, 11)
	if !ok {
		t.Fatal("expected reverse match for backtick")
	}
	if m.ToLine != 0 || m.ToCol != 7 {
		t.Fatalf("expected match at (0, 7), got (%d, %d)", m.ToLine, m.ToCol)
	}
}
