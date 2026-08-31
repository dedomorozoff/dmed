package buffer

import (
	"sort"
	"strings"
)

// Cursor is one of several simultaneous editing positions. From/To define an
// optional per-cursor selection on the same line (From==To means none); the
// caret sits at the end of the selection.
type Cursor struct {
	Line, Col int
	From, To  int
}

// cursorEdit describes a single replacement at a cursor: replace runes
// [from,to) of lines[line] with ins (a slice of lines). An empty ins deletes
// the range. caretLine/caretCol is where the caret should rest afterwards.
type cursorEdit struct {
	line, from, to int
	ins            [][]rune
	caretLine      int
	caretCol       int
}

// HasMultipleCursors reports whether secondary cursors exist.
func (b *Buffer) HasMultipleCursors() bool { return len(b.cursors) > 0 }

// Cursors returns the main cursor followed by all secondary cursors.
func (b *Buffer) Cursors() []Cursor {
	out := make([]Cursor, 0, len(b.cursors)+1)
	out = append(out, Cursor{Line: b.line, Col: b.col})
	out = append(out, b.cursors...)
	return out
}

// ClearCursors drops all secondary cursors and any word target.
func (b *Buffer) ClearCursors() {
	b.cursors = nil
	b.word = ""
}

// AddCursor adds a secondary cursor, returning false if it duplicates an
// existing one (main or secondary).
func (b *Buffer) AddCursor(line, col, from, to int) bool {
	if from < 0 || to < from {
		from, to = col, col
	}
	for _, c := range b.Cursors() {
		if c.Line == line && c.Col == col {
			return false
		}
	}
	b.cursors = append(b.cursors, Cursor{Line: line, Col: col, From: from, To: to})
	return true
}

// isWordRune reports whether r can be part of a word.
func isWordRune(r rune) bool {
	if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
		return true
	}
	return r == '_'
}

// WordAt returns the [from,to) word boundaries on the given line containing
// col, or from==to==col when there is no word there.
func (b *Buffer) WordAt(line, col int) (from, to int) {
	if line < 0 || line >= len(b.lines) {
		return col, col
	}
	l := b.lines[line]
	from, to = col, col
	for from > 0 && isWordRune(l[from-1]) {
		from--
	}
	for to < len(l) && isWordRune(l[to]) {
		to++
	}
	return from, to
}

// NextOccurrence returns the position (line,col) of the next occurrence of
// word strictly after (afterLine, afterCol), searching forward across lines.
func (b *Buffer) NextOccurrence(afterLine, afterCol int, word []rune) (int, int, bool) {
	if len(word) == 0 {
		return 0, 0, false
	}
	w := string(word)
	for ln := afterLine; ln < len(b.lines); ln++ {
		str := string(b.lines[ln])
		start := 0
		if ln == afterLine {
			start = afterCol + 1
		}
		if start > len(str) {
			continue
		}
		idx := strings.Index(str[start:], w)
		if idx >= 0 {
			return ln, start + idx, true
		}
	}
	return 0, 0, false
}

// currentWord returns the selected word, or the word under the main cursor.
func (b *Buffer) currentWord() []rune {
	if b.HasSelection() {
		sl, sc, el, ec := b.SelectionRange()
		if sl == el {
			return append([]rune(nil), b.lines[sl][sc:ec]...)
		}
	}
	f, t := b.WordAt(b.line, b.col)
	return append([]rune(nil), b.lines[b.line][f:t]...)
}

// AddNextOccurrence implements Alt+D: the first call selects the word under
// the main cursor and arms multi mode; each subsequent call adds a cursor at
// the next occurrence of that word. It returns true when something changed.
func (b *Buffer) AddNextOccurrence() bool {
	if b.word == "" {
		f, t := b.WordAt(b.line, b.col)
		if f == t {
			return false
		}
		b.word = string(b.lines[b.line][f:t])
		// Select the word at the main cursor.
		b.hasSelection = true
		b.selLine, b.selCol = b.line, f
		b.col = t
		return true
	}

	lastLine, lastCol := b.line, b.col
	if len(b.cursors) > 0 {
		last := b.cursors[len(b.cursors)-1]
		lastLine, lastCol = last.Line, last.Col
	}
	// The original word (where multi mode was armed) sits at the main
	// selection; reaching it again means we have wrapped fully around.
	originLine, originCol := b.selLine, b.selCol
	stoppedAtOrigin := func(ln, col int) bool {
		return ln == originLine && col == originCol
	}

	ln, col, ok := b.NextOccurrence(lastLine, lastCol, []rune(b.word))
	if !ok {
		// Wrap around from the top; stop once we reach a position that already
		// holds a cursor or the original word so Alt+D terminates.
		ln, col, ok = b.NextOccurrence(0, -1, []rune(b.word))
		if !ok || b.cursorAt(ln, col) || stoppedAtOrigin(ln, col) {
			return false
		}
	} else {
		// Skip positions that already hold a cursor.
		for b.cursorAt(ln, col) || stoppedAtOrigin(ln, col) {
			ln, col, ok = b.NextOccurrence(ln, col, []rune(b.word))
			if !ok {
				ln, col, ok = b.NextOccurrence(0, -1, []rune(b.word))
				if !ok || b.cursorAt(ln, col) || stoppedAtOrigin(ln, col) {
					return false
				}
			}
		}
	}
	f, t := b.WordAt(ln, col)
	b.AddCursor(ln, t, f, t)
	return true
}

func (b *Buffer) cursorAt(line, col int) bool {
	for _, c := range b.Cursors() {
		if c.Line == line && c.Col == col {
			return true
		}
	}
	return false
}

// allPoints flattens the main cursor (with its selection) and all secondary
// cursors into a uniform list of edit points.
func (b *Buffer) allPoints() []Cursor {
	pts := make([]Cursor, 0, len(b.cursors)+1)
	f, t := b.col, b.col
	if b.hasSelection {
		sl, sc, el, ec := b.SelectionRange()
		if sl == el {
			f, t = sc, ec
		}
	}
	pts = append(pts, Cursor{Line: b.line, Col: b.col, From: f, To: t})
	for _, c := range b.cursors {
		cf, ct := c.Col, c.Col
		if c.From != c.To {
			cf, ct = c.From, c.To
		}
		pts = append(pts, Cursor{Line: c.Line, Col: c.Col, From: cf, To: ct})
	}
	return pts
}

// setFromPoints writes back the result cursors, taking the first as the main
// cursor (in list order) and the rest as secondary cursors.
func (b *Buffer) setFromPoints(pts []Cursor) {
	b.ClearSelection()
	if len(pts) == 0 {
		return
	}
	b.SetCursor(pts[0].Line, pts[0].Col)
	b.cursors = b.cursors[:0]
	for _, c := range pts[1:] {
		b.cursors = append(b.cursors, c)
	}
}

// buildEdits converts edit points into insert edits (replacing any selection).
func buildInsertEdits(pts []Cursor, ins [][]rune) []cursorEdit {
	edits := make([]cursorEdit, len(pts))
	for i, p := range pts {
		e := cursorEdit{line: p.Line, from: p.From, to: p.To, ins: ins}
		if len(ins) > 0 {
			e.caretLine = p.Line + len(ins) - 1
			e.caretCol = p.From + len(ins[len(ins)-1])
		} else {
			e.caretLine = p.Line
			e.caretCol = p.From
		}
		edits[i] = e
	}
	return edits
}

// multiApply applies edits bottom-up, correcting carets for same-line edits.
// Edits are expected not to split lines (single-line insertions/deletions).
func (b *Buffer) multiApplyNoSplit(edits []cursorEdit) []Cursor {
	// Group edits by line.
	byLine := map[int][]cursorEdit{}
	var lines []int
	for _, e := range edits {
		if _, ok := byLine[e.line]; !ok {
			lines = append(lines, e.line)
		}
		byLine[e.line] = append(byLine[e.line], e)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(lines)))

	// A caret offset per line to track same-line shifts.
	lineShift := map[int]int{}

	for _, ln := range lines {
		es := byLine[ln]
		// Sort by from ascending so edits are applied left-to-right.
		sort.SliceStable(es, func(i, j int) bool { return es[i].from < es[j].from })
		cur := b.lines[ln]
		var out []rune
		pos := 0
		shift := 0
		for i := range es {
			e := &es[i]
			if e.from < pos {
				e.from = pos
			}
			out = append(out, cur[pos:e.from]...)
			for _, l := range e.ins {
				out = append(out, l...)
			}
			if len(e.ins) > 0 {
				e.caretCol = e.from + len(e.ins[len(e.ins)-1]) + shift
			} else {
				e.caretCol = e.from + shift
			}
			delta := 0
			for _, l := range e.ins {
				delta += len(l)
			}
			delta -= e.to - e.from
			shift += delta
			pos = e.to
		}
		out = append(out, cur[pos:]...)
		b.lines[ln] = out
		lineShift[ln] = shift
	}

	// Apply the accumulated line shift to carets for edits sharing a line.
	res := make([]Cursor, len(edits))
	for i, e := range edits {
		res[i] = Cursor{Line: e.caretLine, Col: e.caretCol}
	}
	return res
}

// MultiInsertText inserts text at every cursor (and replaces per-cursor
// selections) as a single undo step. The main cursor is the first in the list.
func (b *Buffer) MultiInsertText(text string) {
	if !b.HasMultipleCursors() {
		b.InsertText(text)
		return
	}
	b.beginChange()
	b.pushUndo()

	pts := b.allPoints()
	insLines := strings.Split(text, "\n")
	ins := make([][]rune, len(insLines))
	for i, l := range insLines {
		ins[i] = []rune(l)
	}
	edits := buildInsertEdits(pts, ins)
	res := b.multiApplyNoSplit(edits)
	b.setFromPoints(res)
}

// MultiInsertRune inserts a single rune at every cursor (replacing selections).
func (b *Buffer) MultiInsertRune(r rune) {
	if !b.HasMultipleCursors() {
		b.Insert(r)
		return
	}
	b.MultiInsertText(string(r))
}

// MultiNewline splits the line at every cursor as a single undo step.
func (b *Buffer) MultiNewline() {
	if !b.HasMultipleCursors() {
		b.InsertNewline()
		return
	}
	b.beginChange()
	b.pushUndo()

	pts := b.allPoints()
	// Bottom-up so line-splitting below doesn't disturb upper cursors.
	idxs := make([]int, len(pts))
	for i := range idxs {
		idxs[i] = i
	}
	sort.SliceStable(idxs, func(i, j int) bool {
		a, bb := pts[idxs[i]], pts[idxs[j]]
		if a.Line != bb.Line {
			return a.Line > bb.Line
		}
		return a.Col > bb.Col
	})

	res := make([]Cursor, len(pts))
	placed := map[int]bool{}
	for _, ii := range idxs {
		p := pts[ii]
		l := b.lines[p.Line]
		rest := append([]rune(nil), l[p.From:]...)
		b.lines[p.Line] = l[:p.From:p.From]
		b.lines = append(b.lines, nil)
		copy(b.lines[p.Line+2:], b.lines[p.Line+1:])
		b.lines[p.Line+1] = rest
		res[ii] = Cursor{Line: p.Line + 1, Col: 0}
		placed[ii] = true
	}
	b.setFromPoints(res)
}

// MultiBackspace removes the character (or selection) before every cursor.
func (b *Buffer) MultiBackspace() {
	if !b.HasMultipleCursors() {
		b.Backspace()
		return
	}
	b.beginChange()
	b.pushUndo()

	pts := b.allPoints()
	var edits []cursorEdit
	joins := []Cursor{} // cursors at col 0 needing a line join
	for _, p := range pts {
		if p.From != p.To {
			edits = append(edits, cursorEdit{line: p.Line, from: p.From, to: p.To, caretLine: p.Line, caretCol: p.From})
		} else if p.Col > 0 {
			edits = append(edits, cursorEdit{line: p.Line, from: p.Col - 1, to: p.Col, caretLine: p.Line, caretCol: p.Col - 1})
		} else if p.Line > 0 {
			joins = append(joins, Cursor{Line: p.Line, Col: 0})
		}
	}

	if len(edits) > 0 {
		res := b.multiApplyNoSplit(edits)
		// Map results back into join-less cursors and merge with joins.
		bi := 0
		out := make([]Cursor, 0, len(pts))
		for _, p := range pts {
			if p.From != p.To || p.Col > 0 {
				out = append(out, res[bi])
				bi++
			} else if p.Line > 0 {
				out = append(out, Cursor{Line: p.Line, Col: 0})
			}
		}
		// Process joins bottom-up.
		for j := len(joins) - 1; j >= 0; j-- {
			L := joins[j].Line
			if L <= 0 || L >= len(b.lines) {
				continue
			}
			merged := append(append([]rune(nil), b.lines[L-1]...), b.lines[L]...)
			caretCol := len(b.lines[L-1])
			b.lines[L-1] = merged
			b.lines = append(b.lines[:L], b.lines[L+1:]...)
			for k := range out {
				if out[k].Line == L {
					out[k] = Cursor{Line: L - 1, Col: caretCol}
				}
			}
		}
		b.setFromPoints(out)
		return
	}

	// Only joins.
	b.setFromPoints(pts)
}

// MultiDelete removes the character (or selection) at every cursor.
func (b *Buffer) MultiDelete() {
	if !b.HasMultipleCursors() {
		b.Delete()
		return
	}
	b.beginChange()
	b.pushUndo()

	pts := b.allPoints()
	var edits []cursorEdit
	joins := []Cursor{} // cursors at EOL needing a line join
	for _, p := range pts {
		l := b.lines[p.Line]
		if p.From != p.To {
			edits = append(edits, cursorEdit{line: p.Line, from: p.From, to: p.To, caretLine: p.Line, caretCol: p.From})
		} else if p.Col < len(l) {
			edits = append(edits, cursorEdit{line: p.Line, from: p.Col, to: p.Col + 1, caretLine: p.Line, caretCol: p.Col})
		} else if p.Line < len(b.lines)-1 {
			joins = append(joins, Cursor{Line: p.Line, Col: len(l)})
		}
	}

	if len(edits) > 0 {
		res := b.multiApplyNoSplit(edits)
		bi := 0
		out := make([]Cursor, 0, len(pts))
		for _, p := range pts {
			l := b.lines[p.Line]
			if p.From != p.To || p.Col < len(l) {
				out = append(out, res[bi])
				bi++
			} else if p.Line < len(b.lines)-1 {
				out = append(out, Cursor{Line: p.Line, Col: len(l)})
			}
		}
		for j := len(joins) - 1; j >= 0; j-- {
			L := joins[j].Line
			if L < 0 || L >= len(b.lines)-1 {
				continue
			}
			next := b.lines[L+1]
			merged := append(append([]rune(nil), b.lines[L]...), next...)
			b.lines[L] = merged
			b.lines = append(b.lines[:L+1], b.lines[L+2:]...)
			for k := range out {
				if out[k].Line == L {
					out[k] = Cursor{Line: L, Col: out[k].Col}
				}
			}
		}
		b.setFromPoints(out)
		return
	}
	b.setFromPoints(pts)
}

// MoveAllLeft moves every cursor one position left.
func (b *Buffer) MoveAllLeft() {
	if !b.HasMultipleCursors() {
		b.MoveLeft()
		return
	}
	b.moveExtras(func(c Cursor) Cursor {
		if c.Col > 0 {
			c.Col--
		} else if c.Line > 0 {
			c.Line--
			c.Col = len(b.lines[c.Line])
		}
		c.From, c.To = c.Col, c.Col
		return c
	})
	b.MoveLeft()
}

// MoveAllRight moves every cursor one position right.
func (b *Buffer) MoveAllRight() {
	if !b.HasMultipleCursors() {
		b.MoveRight()
		return
	}
	b.moveExtras(func(c Cursor) Cursor {
		if c.Col < len(b.lines[c.Line]) {
			c.Col++
		} else if c.Line < len(b.lines)-1 {
			c.Line++
			c.Col = 0
		}
		c.From, c.To = c.Col, c.Col
		return c
	})
	b.MoveRight()
}

// MoveAllUp moves every cursor up one line (keeping column).
func (b *Buffer) MoveAllUp() {
	if !b.HasMultipleCursors() {
		b.MoveUp()
		return
	}
	b.moveExtras(func(c Cursor) Cursor {
		if c.Line > 0 {
			c.Line--
			if c.Col > len(b.lines[c.Line]) {
				c.Col = len(b.lines[c.Line])
			}
		}
		c.From, c.To = c.Col, c.Col
		return c
	})
	b.MoveUp()
}

// MoveAllDown moves every cursor down one line (keeping column).
func (b *Buffer) MoveAllDown() {
	if !b.HasMultipleCursors() {
		b.MoveDown()
		return
	}
	b.moveExtras(func(c Cursor) Cursor {
		if c.Line < len(b.lines)-1 {
			c.Line++
			if c.Col > len(b.lines[c.Line]) {
				c.Col = len(b.lines[c.Line])
			}
		}
		c.From, c.To = c.Col, c.Col
		return c
	})
	b.MoveDown()
}

func (b *Buffer) moveExtras(f func(Cursor) Cursor) {
	for i := range b.cursors {
		b.cursors[i] = f(b.cursors[i])
	}
}
