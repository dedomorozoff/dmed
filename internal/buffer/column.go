package buffer

import "strings"

// Column (rectangular) selection.
//
// The buffer's selection is already two corners — an anchor (selLine, selCol) and
// a caret (line, col) — so a column block is the same two corners read
// differently: not the span of text from one to the other, but the rectangle they
// bound, one cell per row. That makes the only new state a flag, and the cells are
// derived on every read, clamped to the row they land on.
//
// Deriving is what keeps a block honest. A stored per-row range would point past
// the end of its line as soon as a row changes length — an edit, an external
// reload, or a line-wise command like sort — and the multi-cursor splice slices
// with those columns (applyNoSplit), so a stale one is a panic, not a glitch. The
// rectangle's corner column is the caret's sticky goal column, which clampCol
// never takes below the caret but does let stand past the end of a short row: that
// is exactly the virtual column a block needs to keep its width when a vertical
// grow crosses a short row.
//
// The flag is meaningful only while a selection exists, so the ~20 places that
// clear the selection end the block with it and none of them needed changing. The
// methods that start or extend a *linear* selection clear the flag themselves, so
// a stale one cannot turn an ordinary selection rectangular.

// IsColumnSelection reports whether the selection is read as a rectangle.
func (b *Buffer) IsColumnSelection() bool { return b.hasSelection && b.colSel }

// SetColumnSelection reinterprets the current selection as a rectangle (on) or
// back as the span between its corners (off), and reports the result. With no
// selection there is nothing to make rectangular, so nothing changes.
func (b *Buffer) SetColumnSelection(on bool) bool {
	if on && !b.HasSelection() {
		return false
	}
	b.colSel = on
	if on {
		b.enterColumnCorners()
	}
	return b.colSel
}

// enterColumnCorners makes the caret's column the rectangle's corner: a linear
// selection keeps its ends, a caret resting on a long row reached by a sticky
// vertical move starts the block at the column it stands at, not the one it wants.
func (b *Buffer) enterColumnCorners() {
	if b.goalCol < b.col {
		b.goalCol = b.col
	}
}

// ColumnRows returns the first and last row the rectangle covers, inclusive.
func (b *Buffer) ColumnRows() (from, to int) {
	from, to = b.selLine, b.line
	if from > to {
		from, to = to, from
	}
	return from, to
}

// columnCols returns the rectangle's columns, low first: the anchor's column and
// the caret's goal column.
func (b *Buffer) columnCols() (lo, hi int) {
	lo, hi = b.selCol, b.goalCol
	if lo > hi {
		lo, hi = hi, lo
	}
	return lo, hi
}

// columnCellAt returns the block's cell on the given row. Both ends are clamped to
// that row's length, so a row shorter than the block's left edge holds nothing and
// a row that ends inside the block contributes what it has. A row the rectangle
// does not cover has no cell either.
func (b *Buffer) columnCellAt(line int) (from, to int) {
	first, last := b.ColumnRows()
	if line < first || line > last || line < 0 || line >= b.d.count() {
		return 0, 0
	}
	lo, hi := b.columnCols()
	n := b.lineLen(line)
	if lo >= n {
		return n, n
	}
	if hi > n {
		hi = n
	}
	return lo, hi
}

// ColumnCell is the single-row form the view paints from; ok when the row has
// something to highlight.
func (b *Buffer) ColumnCell(line int) (from, to int, ok bool) {
	if !b.IsColumnSelection() {
		return 0, 0, false
	}
	from, to = b.columnCellAt(line)
	return from, to, from < to
}

// GrowColumn moves the rectangle's caret corner by dRow rows and dCol columns, and
// reports whether anything moved. The first press enters column mode: from a bare
// caret it arms the anchor at the caret, from a linear selection it takes the
// bounding box the corners already make. Growing past the first or the last row
// changes nothing; shrinking the corner onto the anchor leaves no selection at all,
// so the block ends where it began rather than lingering as an empty rectangle.
func (b *Buffer) GrowColumn(dRow, dCol int) bool {
	if !b.colSel {
		if !b.hasSelection {
			b.hasSelection = true
			b.selLine, b.selCol = b.line, b.col
			b.goalCol = b.col
		}
		b.colSel = true
		b.enterColumnCorners()
	}
	moved := false
	if dRow != 0 {
		if ln := b.line + dRow; ln >= 0 && ln < b.d.count() {
			b.line = ln
			// clampCol takes the caret down to a short row and leaves goalCol —
			// the corner — where it was.
			b.clampCol()
			moved = true
		}
	}
	if dCol != 0 {
		col := b.goalCol + dCol
		if col < 0 {
			col = 0
		}
		if col != b.goalCol {
			b.goalCol = col
			if n := b.lineLen(b.line); col > n {
				b.col = n
			} else {
				b.col = col
			}
			moved = true
		}
	}
	if !b.HasSelection() {
		b.hasSelection = false
		b.colSel = false
	}
	return moved
}

// GrowColumnTo moves the rectangle's caret corner to an absolute (line, col)
// and reports whether anything moved. The drag route: the first press enters
// column mode from the caret, then every motion re-aims the corner. The
// column rides the sticky goal column, so a vertical sweep keeps the block's
// width across short rows; a corner dragged onto the anchor ends the block.
func (b *Buffer) GrowColumnTo(line, col int) bool {
	if !b.colSel {
		if line == b.line && col == b.col {
			return false
		}
		b.hasSelection = true
		b.selLine, b.selCol = b.line, b.col
		b.goalCol = b.col
		b.colSel = true
	}
	moved := false
	if line != b.line {
		if line >= 0 && line < b.d.count() {
			b.line = line
			b.clampCol()
			moved = true
		}
	}
	if col != b.goalCol {
		if col < 0 {
			col = 0
		}
		b.goalCol = col
		if n := b.lineLen(b.line); col > n {
			b.col = n
		} else {
			b.col = col
		}
		moved = true
	}
	if !b.HasSelection() {
		b.hasSelection = false
		b.colSel = false
	}
	return moved
}

// columnPoints returns one edit point per covered row, the caret at the end of the
// cell. Every column is clamped here, which is what keeps applyNoSplit from
// slicing past the end of a short row.
func (b *Buffer) columnPoints() []Cursor {
	first, last := b.ColumnRows()
	pts := make([]Cursor, 0, last-first+1)
	for ln := first; ln <= last; ln++ {
		f, t := b.columnCellAt(ln)
		pts = append(pts, Cursor{Line: ln, Col: t, From: f, To: t})
	}
	return pts
}

// columnEdits replaces every cell of the block with ins. One line of ins goes to
// every cell (typing the same text down a column); one line per row distributes
// the block (a rectangular paste), and rows past the last line get nothing. An
// empty ins deletes the cells.
func (b *Buffer) columnEdits(ins [][]rune) []cursorEdit {
	pts := b.columnPoints()
	edits := make([]cursorEdit, 0, len(pts))
	for i, p := range pts {
		line := []rune(nil)
		if len(ins) == 1 {
			line = ins[0]
		} else if len(ins) > 1 {
			if i >= len(ins) {
				break
			}
			line = ins[i]
		}
		edits = append(edits, b.columnEdit(p, line))
	}
	return edits
}

func (b *Buffer) columnEdit(p Cursor, line []rune) cursorEdit {
	return cursorEdit{
		line:      p.Line,
		from:      p.From,
		to:        p.To,
		ins:       [][]rune{line},
		caretLine: p.Line,
		caretCol:  p.From + len(line),
	}
}

// applyColumnEdits replaces the block's cells as a single undo step and leaves a
// caret on every row behind, so the edit is ordinary multi-cursor state from here
// on: the block itself is gone with the selection, and the next keystroke goes
// through the multi-cursor paths in multicursor.go. An edit with nothing to do
// pushes nothing.
func (b *Buffer) applyColumnEdits(edits []cursorEdit) bool {
	if len(edits) == 0 {
		return false
	}
	b.beginChange()
	b.pushUndo()
	b.spliceColumns(edits)
	return true
}

// spliceColumns applies per-row replacements bottom-up in the flat line view, then
// publishes the text and the carets. It is applyNoSplit, which the multi-cursor
// edits already use; the caller owns the undo step.
func (b *Buffer) spliceColumns(edits []cursorEdit) {
	mat := b.linesCopy()
	res := applyNoSplit(mat, edits)
	b.setLines(mat)
	b.setFromPoints(res)
}

// InsertColumnText writes text into every cell of the block — the same text down
// the column, or, when the text has several lines, one line per row.
func (b *Buffer) InsertColumnText(text string) bool {
	parts := strings.Split(text, "\n")
	ins := make([][]rune, len(parts))
	for i, p := range parts {
		ins[i] = []rune(p)
	}
	return b.applyColumnEdits(b.columnEdits(ins))
}

// ColumnBackspace removes the cell on every row, or — where a row's cell is empty,
// which is what a zero-width block made of vertical carets looks like — the
// character before it.
func (b *Buffer) ColumnBackspace() bool {
	return b.applyColumnEdits(b.columnEdgeEdits(-1))
}

// ColumnDelete removes the cell on every row, or the character at it when the cell
// is empty. A row whose column is its end has nothing to the right and is skipped.
func (b *Buffer) ColumnDelete() bool {
	return b.applyColumnEdits(b.columnEdgeEdits(1))
}

// columnEdgeEdits builds the cell deletions. dir is -1 to take the character in
// front of an empty cell and +1 the one after it; the cell itself always wins, so
// a non-empty cell is removed whole.
func (b *Buffer) columnEdgeEdits(dir int) []cursorEdit {
	pts := b.columnPoints()
	edits := make([]cursorEdit, 0, len(pts))
	for _, p := range pts {
		from, to := p.From, p.To
		if from == to {
			n := b.lineLen(p.Line)
			if dir < 0 {
				if from == 0 {
					continue
				}
				from--
			} else {
				if to >= n {
					continue
				}
				to++
			}
		}
		edits = append(edits, cursorEdit{
			line:      p.Line,
			from:      from,
			to:        to,
			caretLine: p.Line,
			caretCol:  from,
		})
	}
	return edits
}

// MapColumnCells folds each cell on its own, so a column of characters changes
// while the rest of every row keeps its text. Rows with nothing in the cell are
// left alone, and a fold that changes nothing pushes no undo step.
func (b *Buffer) MapColumnCells(fold func(string) string) bool {
	pts := b.columnPoints()
	edits := make([]cursorEdit, 0, len(pts))
	changed := false
	for _, p := range pts {
		if p.From == p.To {
			continue
		}
		cell := string(b.d.lineAt(p.Line)[p.From:p.To])
		folded := []rune(fold(cell))
		if string(folded) != cell {
			changed = true
		}
		edits = append(edits, b.columnEdit(p, folded))
	}
	if !changed {
		return false
	}
	return b.applyColumnEdits(edits)
}

// InsertColumn pastes a rectangular copy at the caret: line i of the block goes to
// row i below the caret's row, at the caret's column, clamped to each row. Lines
// with no row left in the document are dropped rather than appended — an unfold
// writes into the text that is there. It leaves a caret on every row it touched.
func (b *Buffer) InsertColumn(lines []string) bool {
	if len(lines) == 0 {
		return false
	}
	edits := make([]cursorEdit, 0, len(lines))
	for i, l := range lines {
		ln := b.line + i
		if ln >= b.d.count() {
			break
		}
		ins := []rune(l)
		col := b.col
		if n := b.lineLen(ln); col > n {
			col = n
		}
		edits = append(edits, cursorEdit{
			line:      ln,
			from:      col,
			to:        col,
			ins:       [][]rune{ins},
			caretLine: ln,
			caretCol:  col + len(ins),
		})
	}
	return b.applyColumnEdits(edits)
}

// EnterColumn replaces the block's cells and splits every covered row at the left
// edge of its cell — Enter inside a column selection, one undo step, with the same
// indent on each new line the way the typed text is repeated on each row.
func (b *Buffer) EnterColumn(indent string) {
	pts := b.columnPoints()
	b.beginChange()
	b.pushUndo()
	mat := b.linesCopy()
	res := make([]Cursor, len(pts))
	ind := []rune(indent)
	// Bottom-up, so the rows above keep the indices the next edit uses.
	for i := len(pts) - 1; i >= 0; i-- {
		p := pts[i]
		l := mat[p.Line]
		head := append([]rune(nil), l[:p.From]...)
		tail := append(append([]rune(nil), ind...), l[p.To:]...)
		mat[p.Line] = head[:len(head):len(head)]
		mat = append(mat, nil)
		copy(mat[p.Line+2:], mat[p.Line+1:])
		mat[p.Line+1] = tail
		res[i] = Cursor{Line: p.Line + 1, Col: len(ind)}
	}
	b.setLines(mat)
	b.setFromPoints(res)
}

// ColumnText is the block's cells joined by newlines — what a copy of a column
// selection puts in the clipboard, one line per row, so pasting it can unfold it
// back down a column.
func (b *Buffer) ColumnText() string {
	first, last := b.ColumnRows()
	parts := make([]string, 0, last-first+1)
	for ln := first; ln <= last; ln++ {
		f, t := b.columnCellAt(ln)
		parts = append(parts, string(b.d.lineAt(ln)[f:t]))
	}
	return strings.Join(parts, "\n")
}
