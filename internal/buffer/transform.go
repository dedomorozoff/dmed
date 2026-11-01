package buffer

import (
	"sort"
	"strconv"
	"strings"
)

// Selection transformations: case, sort, trim, indent alignment and list
// numbering. They follow the shape the other line-range operations use
// (IndentLines, ToggleComment, MoveLineUp): beginChange + pushUndo, copy the
// lines out, rewrite the block, publish it. Each command is one undo step.
//
// None of them changes the line count, so the selection keeps covering the same
// lines and only the caret column has to be clamped — see commitRange.

// SelectionLines returns the whole lines the selection touches, or the cursor
// line when there is no selection. Line-wise commands work on whole lines: a
// block sort that respected the selection's columns would sort half a line.
func (b *Buffer) SelectionLines() (from, to int) {
	if !b.HasSelection() {
		return b.line, b.line
	}
	sl, _, el, _ := b.SelectionRange()
	return sl, el
}

// commitRange publishes a rewritten block of lines whose count did not change.
func (b *Buffer) commitRange(mat [][]rune) {
	b.setLines(mat)
	b.col = clampCol(b.col, b.d.lineAt(b.line))
	b.selCol = clampCol(b.selCol, b.d.lineAt(b.selLine))
	b.goalCol = b.col
}

// isBlank reports a line that holds nothing but whitespace.
func isBlank(line []rune) bool { return leadingWS(line) == len(line) }

// ApplyCase folds the selected characters, or every character in the buffer when
// there is no selection. The whole-buffer path folds line by line instead of
// reloading the text, because buffer.Load would drop the undo history, the
// selection and the caret along with it.
func (b *Buffer) ApplyCase(toUpper bool) {
	fold := strings.ToLower
	if toUpper {
		fold = strings.ToUpper
	}
	if b.HasSelection() {
		sl, sc, el, ec := b.SelectionRange()
		b.ReplaceSpan(sl, sc, el, ec, fold(b.SelectedText()))
		return
	}
	b.beginChange()
	b.pushUndo()
	mat := b.linesCopy()
	for i, line := range mat {
		mat[i] = []rune(fold(string(line)))
	}
	b.commitRange(mat)
}

// SortLines permutes the lines [from..to] and reports how many lines the block
// holds, or 0 when they were already in order.
//
// The comparison folds case first, so a mixed-case list comes out alphabetical
// the way people read it, and falls back to the exact runes so the order is
// total. Whitespace-only lines go to the end of the block: a blank line at the
// top of a sorted list looks like a bug rather than like content.
func (b *Buffer) SortLines(from, to int) int {
	if from < 0 || to <= from || to >= b.d.count() {
		return 0
	}
	before := b.groupLines(from, to)
	after := make([][]rune, len(before))
	copy(after, before)
	sort.SliceStable(after, func(i, j int) bool { return lessSortedLine(after[i], after[j]) })

	moved := 0
	for i := range after {
		if string(after[i]) != string(before[i]) {
			moved++
		}
	}
	if moved == 0 {
		return 0
	}
	b.beginChange()
	b.pushUndo()
	mat := b.linesCopy()
	copy(mat[from:to+1], after)
	b.commitRange(mat)
	return len(after)
}

func lessSortedLine(la, lb []rune) bool {
	blankA, blankB := isBlank(la), isBlank(lb)
	if blankA != blankB {
		return !blankA
	}
	foldA, foldB := strings.ToLower(string(la)), strings.ToLower(string(lb))
	if foldA != foldB {
		return foldA < foldB
	}
	return string(la) < string(lb)
}

// TrimTrailing removes spaces and tabs from the end of the lines [from..to] and
// reports how many lines changed. Leading whitespace is indentation and the
// block's blank lines are content, so neither is touched.
func (b *Buffer) TrimTrailing(from, to int) int {
	if from < 0 || to >= b.d.count() {
		return 0
	}
	mat := b.linesCopy()
	changed := 0
	for i := from; i <= to; i++ {
		line := mat[i]
		end := len(line)
		for end > 0 && (line[end-1] == ' ' || line[end-1] == '\t') {
			end--
		}
		if end == len(line) {
			continue
		}
		mat[i] = append([]rune(nil), line[:end]...)
		changed++
	}
	if changed == 0 {
		return 0
	}
	b.beginChange()
	b.pushUndo()
	b.commitRange(mat)
	return changed
}

// AlignIndent moves the content of every non-blank line in [from..to] to the
// deepest content column the block already has, and reports how many lines it
// moved.
//
// It only ever prepends spaces, never rewrites what a line starts with: the
// depth is measured in expanded columns (a tab is tabW wide), so a tab-indented
// line that is already at the target keeps its tabs, and a shallower one gets the
// difference in spaces in front of its own indent.
func (b *Buffer) AlignIndent(from, to, tabW int) int {
	if from < 0 || to >= b.d.count() {
		return 0
	}
	target := 0
	for i := from; i <= to; i++ {
		if w := b.d.indentWidth(i, tabW); w > target {
			target = w
		}
	}
	if target == 0 {
		return 0
	}
	mat := b.linesCopy()
	changed := 0
	for i := from; i <= to; i++ {
		w := b.d.indentWidth(i, tabW)
		if w < 0 || w >= target { // blank, or already deep enough
			continue
		}
		pad := make([]rune, target-w)
		for k := range pad {
			pad[k] = ' '
		}
		mat[i] = append(pad, mat[i]...)
		changed++
	}
	if changed == 0 {
		return 0
	}
	b.beginChange()
	b.pushUndo()
	b.commitRange(mat)
	return changed
}

// NumberLines prefixes the non-blank lines [from..to] with "1. ", "2. " … after
// their indentation and reports how many lines it numbered.
//
// A blank line is skipped and does not consume a number. An existing leading
// "N." / "N)" marker is replaced rather than stacked, so running the command
// twice on the same block still yields 1..N; a line whose whole content is such a
// marker is left alone, because there is nothing there to number.
func (b *Buffer) NumberLines(from, to int) int {
	if from < 0 || to >= b.d.count() {
		return 0
	}
	mat := b.linesCopy()
	numbered, changed := 0, 0
	for i := from; i <= to; i++ {
		line := mat[i]
		if isBlank(line) {
			continue
		}
		ind := leadingWS(line)
		body, hadMarker := skipListMarker(line[ind:])
		if hadMarker && len(body) == 0 {
			continue
		}
		numbered++
		nl := make([]rune, 0, ind+len(strconv.Itoa(numbered))+2+len(body))
		nl = append(nl, line[:ind]...)
		nl = append(nl, []rune(strconv.Itoa(numbered))...)
		nl = append(nl, '.', ' ')
		nl = append(nl, body...)
		if string(nl) != string(line) {
			mat[i] = nl
			changed++
		}
	}
	if changed > 0 {
		b.beginChange()
		b.pushUndo()
		b.commitRange(mat)
	}
	return numbered
}

// skipListMarker drops a leading "12." or "12)" (and the whitespace after it)
// from the line's content. It returns the content and whether a marker was
// there; a bare number with no punctuation is content, not a marker.
func skipListMarker(body []rune) ([]rune, bool) {
	i := 0
	for i < len(body) && body[i] >= '0' && body[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(body) || (body[i] != '.' && body[i] != ')') {
		return body, false
	}
	i++
	for i < len(body) && (body[i] == ' ' || body[i] == '\t') {
		i++
	}
	return body[i:], true
}
