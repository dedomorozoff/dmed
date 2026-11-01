package editor

import (
	"unicode"

	"dmed/internal/buffer"
)

// Typing niceties: auto-close brackets/quote, type-over, and the smart-indent
// continuation for Enter. They live in the editor, not the buffer: they are UI
// policy that peeks at the immediate characters and routes through plain
// buffer primitives (single-cursor only — multi-cursor typing keeps the plain
// path).

// bracketClose maps an opening bracket to its partner.
var bracketClose = map[rune]rune{'(': ')', '[': ']', '{': '}'}

func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// insertTyped inserts one typed rune with auto-close: '(' becomes "()",
// '[' "[]", '{' "{}", '"' `""`. Typing the closer itself (or a matching
// quote) over an identical character right after the caret steps over it
// instead of duplicating. A quote does not open a pair when it would hug a
// word on either side (apostrophes in prose, "don't"-style text) or follows a
// backslash the user wrote for their own escape.
func insertTyped(b *buffer.Buffer, r rune) {
	line := []rune(string(b.LineAt(b.CurLine())))
	col := b.Col()
	if col > len(line) {
		col = len(line)
	}
	var next, prev rune
	if col < len(line) {
		next = line[col]
	}
	if col > 0 {
		prev = line[col-1]
	}

	// Type-over: the closer (or the matching quote) is already in place.
	if r == next && (r == ')' || r == ']' || r == '}' || r == '"') {
		b.MoveRight()
		return
	}
	if close, ok := bracketClose[r]; ok {
		b.Insert(r)
		b.Insert(close) // typing-run grouping folds the pair into one undo
		b.MoveLeft()
		return
	}
	if r == '"' && prev != '\\' && prev != '"' && !isWordChar(prev) && !isWordChar(next) {
		b.Insert(r)
		b.Insert('"')
		b.MoveLeft()
		return
	}
	b.Insert(r)
}

// deletePair backspaces away the whole empty pair when the caret sits
// precisely between matched characters — "()"|" becomes "| — one undo step,
// like every editor that auto-closes. It reports whether it handled the key.
func deletePair(b *buffer.Buffer) bool {
	line := []rune(string(b.LineAt(b.CurLine())))
	col := b.Col()
	if col < 1 || col >= len(line) {
		return false
	}
	open, close := line[col-1], line[col]
	if close == '"' && open == '"' {
		return b.ReplaceRange(b.CurLine(), col-1, 2, []rune{})
	}
	if bracketClose[open] == close {
		return b.ReplaceRange(b.CurLine(), col-1, 2, []rune{})
	}
	return false
}

// smartIndent computes the continuation indentation for a newline: the
// current line's leading whitespace, one tab deeper when the text before the
// caret ends with an open bracket. (Enter at column 0 keeps the line's own
// indent: pure-indent prefixes become empty lines, as expected.)
func (m *Model) smartIndent() string {
	t := m.cur()
	line := t.buf.LineAt(t.buf.CurLine())
	col := t.buf.Col()
	if col > len(line) {
		col = len(line)
	}
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	indent := string(line[:i])
	for j := col - 1; j >= i; j-- {
		r := line[j]
		if r == ' ' || r == '\t' {
			continue
		}
		switch r {
		case '(', '[', '{':
			indent += "\t"
		}
		break
	}
	return indent
}
