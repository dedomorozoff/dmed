package buffer

import (
	"strings"
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

func TestEnclosingBracketFindsTheInnermostPair(t *testing.T) {
	// f( g( h ) )
	b := Load("f(g(h))\n")

	m, ok := b.enclosingBracket(0, 4)
	if !ok {
		t.Fatal("expected the inner pair to enclose the cursor")
	}
	if m.FromLine != 0 || m.FromCol != 3 || m.ToLine != 0 || m.ToCol != 5 {
		t.Fatalf("innermost pair = %+v, want (0,3)..(0,5)", m)
	}
}

func TestEnclosingBracketIgnoresAPairClosedToTheLeft(t *testing.T) {
	b := Load("f(a) + g(b)\n")

	// The cursor is inside g's argument, so f's paren must not win: the
	// closer at col 3 raises a debt the opener at col 1 pays.
	m, ok := b.enclosingBracket(0, 9)
	if !ok {
		t.Fatal("expected g's pair to enclose the cursor")
	}
	if m.FromCol != 8 || m.ToCol != 10 {
		t.Fatalf("enclosing pair = %+v, want cols 8..10", m)
	}

	// A pair that closed before the cursor is not an enclosing one at all.
	b = Load("f(a) x\n")
	if _, ok := b.enclosingBracket(0, 5); ok {
		t.Fatal("f(a) is closed to the left of the cursor")
	}
}

func TestEnclosingBracketSkipsEscapedDelimiters(t *testing.T) {
	// f( g \)  h ) — the escaped closer inside must not be read as a closer.
	b := Load(`f(g\) h)` + "\n")

	m, ok := b.enclosingBracket(0, 6)
	if !ok {
		t.Fatal(`expected f's pair to enclose the cursor past "\)"`)
	}
	if m.FromCol != 1 || m.ToCol != 7 {
		t.Fatalf("enclosing pair = %+v, want cols 1..7", m)
	}
}

func TestEnclosingBracketAcrossLinesAndPastTheWindow(t *testing.T) {
	closerFor := func(n int) string {
		var sb strings.Builder
		sb.WriteString("f(\n")
		sb.WriteString(strings.Repeat("x\n", n))
		sb.WriteString("  cursor\n)\n")
		return sb.String()
	}

	// Opened 300 lines above: still resolves, and to the right coordinates.
	b := Load(closerFor(299))
	m, ok := b.enclosingBracket(300, 3)
	if !ok {
		t.Fatal("expected a pair opened 300 lines above to resolve")
	}
	if m.FromLine != 0 || m.FromCol != 1 || m.ToLine != 301 || m.ToCol != 0 {
		t.Fatalf("enclosing pair = %+v, want (0,1)..(301,0)", m)
	}

	// Beyond the scan window the opener is never seen, so the answer is "no
	// pair" rather than a partner guessed somewhere else.
	b = Load(closerFor(2100))
	if _, ok := b.enclosingBracket(2101, 3); ok {
		t.Fatalf("a pair opened past maxScanLines=%d must not resolve", maxScanLines)
	}
}

func TestMatchingDelimiterCrossesToThePartnerWhenOnADelimiter(t *testing.T) {
	b := Load("foo(bar)\n" + `q := "hi"` + "\n")

	cases := []struct {
		name              string
		line, col         int
		wantLine, wantCol int
	}{
		{"open paren", 0, 3, 0, 7},
		{"close paren", 0, 7, 0, 3},
		{"just after close paren", 0, 8, 0, 3},
		{"open quote", 1, 5, 1, 8},
		{"close quote", 1, 8, 1, 5},
	}
	for _, c := range cases {
		l2, c2, ok := b.MatchingDelimiter(c.line, c.col)
		if !ok {
			t.Fatalf("%s: expected a partner", c.name)
		}
		if l2 != c.wantLine || c2 != c.wantCol {
			t.Fatalf("%s: partner = (%d,%d), want (%d,%d)", c.name, l2, c2, c.wantLine, c.wantCol)
		}
	}
}

func TestMatchingDelimiterFromInsideGoesToTheCloser(t *testing.T) {
	b := Load("foo(bar)\n")

	// The cursor sits on 'a', with no delimiter under it or before it, so only
	// the enclosing scan can answer — and it answers with the closer.
	l2, c2, ok := b.MatchingDelimiter(0, 5)
	if !ok {
		t.Fatal("expected the enclosing pair's closer")
	}
	if l2 != 0 || c2 != 7 {
		t.Fatalf("target = (%d,%d), want (0,7)", l2, c2)
	}

	// Landing on the closer means the same chord crosses back to the opener,
	// which is why there is no separate backward command.
	l2, c2, ok = b.MatchingDelimiter(0, 7)
	if !ok || l2 != 0 || c2 != 3 {
		t.Fatalf("second press = (%d,%d,%v), want (0,3,true)", l2, c2, ok)
	}
}

func TestMatchingDelimiterRefusesOutsideAnyPair(t *testing.T) {
	b := Load("plain text here\nand (more\n")

	if _, _, ok := b.MatchingDelimiter(0, 6); ok {
		t.Fatal("no delimiter anywhere near a plain line")
	}
	// The paren never closes, so neither the pair nor the enclosing scan has an
	// answer to give.
	if _, _, ok := b.MatchingDelimiter(1, 5); ok {
		t.Fatal("an unclosed bracket has no partner")
	}
	if b.Text() != "plain text here\nand (more\n" {
		t.Fatalf("a refusal must not touch the text: %q", b.Text())
	}
}

func TestInsidePairExcludesTheDelimiters(t *testing.T) {
	b := Load("foo(bar)\n")

	for _, col := range []int{3, 7, 5} {
		sl, sc, el, ec, ok := b.InsidePair(0, col)
		if !ok {
			t.Fatalf("col %d: expected the pair's content", col)
		}
		if sl != 0 || sc != 4 || el != 0 || ec != 7 {
			t.Fatalf("col %d: range = (%d,%d)..(%d,%d), want (0,4)..(0,7)", col, sl, sc, el, ec)
		}
	}

	b.SetCursor(0, 0)
	sl, sc, el, ec, _ := b.InsidePair(0, 5)
	b.SelectRange(sl, sc, el, ec)
	if got := b.SelectedText(); got != "bar" {
		t.Fatalf("SelectedText = %q, want \"bar\"", got)
	}
}

func TestInsidePairOfAnEmptyPair(t *testing.T) {
	b := Load("foo()\n")

	// On the opener, on the closer and between them all describe the same empty
	// middle: start == end, and still a found pair.
	for _, col := range []int{3, 4} {
		sl, sc, el, ec, ok := b.InsidePair(0, col)
		if !ok {
			t.Fatalf("col %d: an empty pair is still a pair", col)
		}
		if sl != el || sc != ec || sl != 0 || sc != 4 {
			t.Fatalf("col %d: range = (%d,%d)..(%d,%d), want an empty (0,4)", col, sl, sc, el, ec)
		}
	}
}

func TestInsidePairOnAQuoteSelectsTheStringBody(t *testing.T) {
	b := Load(`q := "hi"` + "\n")

	sl, sc, el, ec, ok := b.InsidePair(0, 5)
	if !ok {
		t.Fatal("expected the string body")
	}
	if sl != 0 || sc != 6 || el != 0 || ec != 8 {
		t.Fatalf("range = (%d,%d)..(%d,%d), want (0,6)..(0,8)", sl, sc, el, ec)
	}
	b.SelectRange(sl, sc, el, ec)
	if got := b.SelectedText(); got != "hi" {
		t.Fatalf("SelectedText = %q, want \"hi\"", got)
	}
}

func TestInsidePairAcrossLines(t *testing.T) {
	b := Load("f(\n  a\n)\n")

	sl, sc, el, ec, ok := b.InsidePair(1, 2)
	if !ok {
		t.Fatal("expected the multi-line pair's content")
	}
	if sl != 0 || sc != 2 || el != 2 || ec != 0 {
		t.Fatalf("range = (%d,%d)..(%d,%d), want (0,2)..(2,0)", sl, sc, el, ec)
	}
}

func TestSelectRangeSetsAnchorAndCaret(t *testing.T) {
	b := Load("aaaaaaaa\nbbbbccbb\n")

	// A sticky goal column of 1: DragSelect would re-clamp the caret to it and
	// silently move the end of the range the command just computed.
	b.SetCursor(0, 0)
	b.MoveRight()

	b.SelectRange(1, 4, 1, 6)
	if b.CurLine() != 1 || b.Col() != 6 {
		t.Fatalf("caret = (%d,%d), want (1,6)", b.CurLine(), b.Col())
	}
	if !b.HasSelection() {
		t.Fatal("expected a selection")
	}
	if sl, sc, el, ec := b.SelectionRange(); sl != 1 || sc != 4 || el != 1 || ec != 6 {
		t.Fatalf("range = (%d,%d)..(%d,%d), want (1,4)..(1,6)", sl, sc, el, ec)
	}
	if got := b.SelectedText(); got != "cc" {
		t.Fatalf("SelectedText = %q, want \"cc\"", got)
	}

	// The ends may arrive in either order; the anchor is always the start.
	b.SelectRange(1, 6, 1, 4)
	if sl, sc, _, ec := b.SelectionRange(); sl != 1 || sc != 4 || ec != 6 {
		t.Fatalf("reversed range = (%d,%d)..(,%d), want (1,4)..(1,6)", sl, sc, ec)
	}
	if b.CurLine() != 1 || b.Col() != 6 {
		t.Fatalf("caret after a reversed range = (%d,%d), want (1,6)", b.CurLine(), b.Col())
	}
}

func TestSelectRangeClampsPastEndOfLine(t *testing.T) {
	b := Load("short\nlonger line here\n")

	b.SelectRange(0, 2, 0, 99)
	if b.CurLine() != 0 || b.Col() != 5 {
		t.Fatalf("caret = (%d,%d), want clamped to (0,5)", b.CurLine(), b.Col())
	}
	if got := b.SelectedText(); got != "ort" {
		t.Fatalf("SelectedText = %q, want \"ort\"", got)
	}
	if b.goalCol != 5 {
		t.Fatalf("goalCol = %d, want the caret column 5", b.goalCol)
	}
}

func TestSelectRangeEmptyRangeLeavesNoSelection(t *testing.T) {
	b := Load("hello\n")

	b.SelectRange(0, 3, 0, 3)
	if b.HasSelection() {
		t.Fatal("an empty range must not report a selection")
	}
	if b.CurLine() != 0 || b.Col() != 3 {
		t.Fatalf("caret = (%d,%d), want (0,3)", b.CurLine(), b.Col())
	}
}
