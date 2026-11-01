package buffer

// DelimiterMatch records the locations of a matching pair of brackets or quotes.
type DelimiterMatch struct {
	FromLine int
	FromCol  int
	ToLine   int
	ToCol    int
}

// maxScanLines is the search limit for matching brackets across lines to
// avoid stalling on giant documents with unclosed brackets.
const maxScanLines = 2000

var (
	bracketPairs = map[rune]rune{
		'(': ')',
		'[': ']',
		'{': '}',
	}
	bracketReverse = map[rune]rune{
		')': '(',
		']': '[',
		'}': '{',
	}
)

func isQuote(r rune) bool {
	return r == '"' || r == '\'' || r == '`'
}

func isBracket(r rune) bool {
	_, open := bracketPairs[r]
	_, close := bracketReverse[r]
	return open || close
}

// isEscaped reports whether the rune at col on line is escaped by an odd number
// of preceding backslashes.
func isEscaped(line []rune, col int) bool {
	slashes := 0
	for i := col - 1; i >= 0 && line[i] == '\\'; i-- {
		slashes++
	}
	return slashes%2 != 0
}

// MatchDelimiter finds the matching bracket or quote for the delimiter at or
// immediately preceding (line, col). It returns the pair coordinates and true,
// or false if no delimiter is at/near the position or no partner exists.
func (b *Buffer) MatchDelimiter(line, col int) (DelimiterMatch, bool) {
	if line < 0 || line >= b.LineCount() {
		return DelimiterMatch{}, false
	}
	raw := b.LineAt(line)
	if len(raw) == 0 {
		return DelimiterMatch{}, false
	}

	targetCol := -1
	if col < len(raw) && (isBracket(raw[col]) || isQuote(raw[col])) {
		targetCol = col
	} else if col > 0 && col-1 < len(raw) && (isBracket(raw[col-1]) || isQuote(raw[col-1])) {
		targetCol = col - 1
	}

	if targetCol < 0 {
		return DelimiterMatch{}, false
	}

	r := raw[targetCol]
	if isEscaped(raw, targetCol) {
		return DelimiterMatch{}, false
	}

	// 1. Quotes
	if isQuote(r) {
		return b.matchQuote(line, targetCol, r)
	}

	// 2. Opening bracket
	if closer, ok := bracketPairs[r]; ok {
		return b.matchForwardBracket(line, targetCol, r, closer)
	}

	// 3. Closing bracket
	if opener, ok := bracketReverse[r]; ok {
		return b.matchBackwardBracket(line, targetCol, opener, r)
	}

	return DelimiterMatch{}, false
}

func (b *Buffer) matchQuote(line, col int, q rune) (DelimiterMatch, bool) {
	raw := b.LineAt(line)

	// Single or double quote: match on the same line
	if q == '"' || q == '\'' {
		var quotes []int
		for i, ch := range raw {
			if ch == q && !isEscaped(raw, i) {
				// Skip apostrophes in words like don't
				if q == '\'' && i > 0 && i+1 < len(raw) && isWordRune(raw[i-1]) && isWordRune(raw[i+1]) {
					continue
				}
				quotes = append(quotes, i)
			}
		}
		for i := 0; i+1 < len(quotes); i += 2 {
			open, close := quotes[i], quotes[i+1]
			if col == open {
				return DelimiterMatch{FromLine: line, FromCol: col, ToLine: line, ToCol: close}, true
			}
			if col == close {
				return DelimiterMatch{FromLine: line, FromCol: col, ToLine: line, ToCol: open}, true
			}
		}
		return DelimiterMatch{}, false
	}

	// Backtick: can be multiline
	// First check if matched on the same line
	var bts []int
	for i, ch := range raw {
		if ch == '`' && !isEscaped(raw, i) {
			bts = append(bts, i)
		}
	}
	for i := 0; i+1 < len(bts); i += 2 {
		open, close := bts[i], bts[i+1]
		if col == open {
			return DelimiterMatch{FromLine: line, FromCol: col, ToLine: line, ToCol: close}, true
		}
		if col == close {
			return DelimiterMatch{FromLine: line, FromCol: col, ToLine: line, ToCol: open}, true
		}
	}

	// If unclosed on this line, determine whether it's an opener or closer
	// by counting previous backticks in a window of preceding lines.
	startLine := line - 300
	if startLine < 0 {
		startLine = 0
	}
	prevCount := 0
	for l := startLine; l < line; l++ {
		lraw := b.LineAt(l)
		for i, ch := range lraw {
			if ch == '`' && !isEscaped(lraw, i) {
				prevCount++
			}
		}
	}
	for i, ch := range raw[:col] {
		if ch == '`' && !isEscaped(raw, i) {
			prevCount++
		}
	}

	if prevCount%2 == 0 {
		// Even count before us -> this is an OPENER. Scan forward for the next backtick.
		endLine := line + maxScanLines
		if endLine > b.LineCount() {
			endLine = b.LineCount()
		}
		for l := line; l < endLine; l++ {
			lraw := b.LineAt(l)
			startC := 0
			if l == line {
				startC = col + 1
			}
			for i := startC; i < len(lraw); i++ {
				if lraw[i] == '`' && !isEscaped(lraw, i) {
					return DelimiterMatch{FromLine: line, FromCol: col, ToLine: l, ToCol: i}, true
				}
			}
		}
	} else {
		// Odd count before us -> this is a CLOSER. Scan backward for the previous backtick.
		endLine := line - maxScanLines
		if endLine < 0 {
			endLine = 0
		}
		for l := line; l >= endLine; l-- {
			lraw := b.LineAt(l)
			endC := len(lraw) - 1
			if l == line {
				endC = col - 1
			}
			for i := endC; i >= 0; i-- {
				if lraw[i] == '`' && !isEscaped(lraw, i) {
					return DelimiterMatch{FromLine: line, FromCol: col, ToLine: l, ToCol: i}, true
				}
			}
		}
	}

	return DelimiterMatch{}, false
}

func (b *Buffer) matchForwardBracket(line, col int, open, close rune) (DelimiterMatch, bool) {
	depth := 1
	endLine := line + maxScanLines
	if endLine > b.LineCount() {
		endLine = b.LineCount()
	}

	for l := line; l < endLine; l++ {
		raw := b.LineAt(l)
		startCol := 0
		if l == line {
			startCol = col + 1
		}
		for c := startCol; c < len(raw); c++ {
			if isEscaped(raw, c) {
				continue
			}
			ch := raw[c]
			if ch == open {
				depth++
			} else if ch == close {
				depth--
				if depth == 0 {
					return DelimiterMatch{FromLine: line, FromCol: col, ToLine: l, ToCol: c}, true
				}
			}
		}
	}
	return DelimiterMatch{}, false
}

func (b *Buffer) matchBackwardBracket(line, col int, open, close rune) (DelimiterMatch, bool) {
	depth := 1
	startLine := line - maxScanLines
	if startLine < 0 {
		startLine = 0
	}

	for l := line; l >= startLine; l-- {
		raw := b.LineAt(l)
		endCol := len(raw) - 1
		if l == line {
			endCol = col - 1
		}
		for c := endCol; c >= 0; c-- {
			if isEscaped(raw, c) {
				continue
			}
			ch := raw[c]
			if ch == close {
				depth++
			} else if ch == open {
				depth--
				if depth == 0 {
					return DelimiterMatch{FromLine: line, FromCol: col, ToLine: l, ToCol: c}, true
				}
			}
		}
	}
	return DelimiterMatch{}, false
}
