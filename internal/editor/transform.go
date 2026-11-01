package editor

// The selection-transformation commands. Each one picks the target the operation
// works on, calls the buffer method that does the text work (see
// internal/buffer/transform.go) and reports the outcome in the status bar.
//
// Secondary cursors are dropped before the edit: these are block operations that
// permute, pad or renumber whole lines, so a stored cursor would end up pointing
// at text that is no longer its own. The selection survives, because none of the
// operations changes the line count — so the same block can be transformed again
// without selecting it twice.

func (m *Model) uppercaseActive() {
	m.foldCase(true)
}

func (m *Model) lowercaseActive() {
	m.foldCase(false)
}

// foldCase folds the selection, or the whole buffer when there is none — the
// rule the command has always had, now without reloading the buffer and losing
// the undo history with it.
func (m *Model) foldCase(toUpper bool) {
	b := m.cur().buf
	b.ClearCursors()
	b.ApplyCase(toUpper)
	if toUpper {
		m.msg = m.t("msg.uppercased")
	} else {
		m.msg = m.t("msg.lowercased")
	}
}

func (m *Model) sortLinesActive() {
	b := m.cur().buf
	from, to := b.SelectionLines()
	b.ClearCursors()
	m.msg = m.transformMsg("msg.sorted_lines", b.SortLines(from, to))
}

// trimTrailingActive strips trailing whitespace. With no selection it runs over
// the whole buffer: that is the cleanup people want before a commit, and
// trimming the single line under the cursor is almost never it.
func (m *Model) trimTrailingActive() {
	b := m.cur().buf
	from, to := b.SelectionLines()
	if !b.HasSelection() {
		from, to = 0, b.LineCount()-1
	}
	b.ClearCursors()
	m.msg = m.transformMsg("msg.trimmed_lines", b.TrimTrailing(from, to))
}

func (m *Model) alignIndentActive() {
	b := m.cur().buf
	from, to := b.SelectionLines()
	b.ClearCursors()
	m.msg = m.transformMsg("msg.aligned_lines", b.AlignIndent(from, to, m.cfg.Editor.TabWidth))
}

func (m *Model) numberLinesActive() {
	b := m.cur().buf
	from, to := b.SelectionLines()
	b.ClearCursors()
	m.msg = m.transformMsg("msg.numbered_lines", b.NumberLines(from, to))
}

// transformMsg reports how many lines a transformation touched, or that there
// was nothing to do — "trimmed 0 lines" teaches the user nothing.
func (m *Model) transformMsg(key string, n int) string {
	if n == 0 {
		return m.t("msg.transform_unchanged")
	}
	return m.t(key, n)
}
