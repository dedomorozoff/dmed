package editor

// The two bracket-navigation commands. Both resolve the pair the cursor belongs
// to (internal/buffer/match.go does the scanning) and then only move the cursor
// or the selection — nothing is edited, so neither pushes an undo step.
//
// There is no backward chord: Ctrl+[ *is* Escape on every terminal, so the pair
// has to be walkable with one key. It is — jumpToMatchingDelimiterActive always
// lands the cursor *on* a delimiter, and from there the same chord returns to
// the partner, so pressing it twice is back where you started.
//
// Secondary cursors are dropped first: both commands act on one region, and a
// stored cursor would be left pointing at text the jump never intended.

// jumpToMatchingDelimiterActive moves the cursor to the matching delimiter: the
// partner of the one it sits on (brackets and quotes), or the closing delimiter
// of the innermost bracket pair it sits inside. With no pair anywhere the cursor
// stays exactly where it is.
func (m *Model) jumpToMatchingDelimiterActive() {
	t := m.cur()
	if t == nil {
		return
	}
	b := t.buf
	b.ClearCursors()
	line, col := b.CurLine(), b.Col()
	l2, c2, ok := b.MatchingDelimiter(line, col)
	if !ok {
		m.msg = m.t("msg.bracket_no_match")
		return
	}
	b.SetCursor(l2, c2)
	b.Deselect()
	m.clampScroll()
	m.msg = ""
}

// selectInsideBracketsActive selects the text strictly between the pair the
// cursor belongs to, delimiters excluded, in one step. An empty pair is not a
// refusal: the cursor moves between the delimiters and the message says the
// middle is empty.
func (m *Model) selectInsideBracketsActive() {
	t := m.cur()
	if t == nil {
		return
	}
	b := t.buf
	b.ClearCursors()
	sl, sc, el, ec, ok := b.InsidePair(b.CurLine(), b.Col())
	if !ok {
		m.msg = m.t("msg.bracket_no_match")
		return
	}
	b.SelectRange(sl, sc, el, ec)
	m.clampScroll()
	if !b.HasSelection() {
		m.msg = m.t("msg.bracket_empty")
		return
	}
	m.msg = ""
}
