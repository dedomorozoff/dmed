package editor

// Go to the position this file was last changed at.
//
// The position is the buffer's, because the buffer is the one place every editing
// method already passes through: each one pushes an undo step, and the undo
// snapshot takes the caret with it. So typing, the line-wise commands, the
// transformations, the multi-cursor edits and redo are all answered without a
// second implementation of "did this change text".
//
// The command is a jump: it leaves a position the user cannot walk back to, so it
// records that position for Ctrl+Alt+← before moving. Pressing it twice is a
// refusal instead — the caret is already on the change, and a jump-list slot spent
// on a move that does nothing would make the next back press look broken.

// goToLastEdit lands the caret on the last change in the current file. SetCursor
// clamps line and column, so a buffer that shrank after the edit cannot strand
// it, and clampScroll unfolds a block hiding the landing line.
func (m *Model) goToLastEdit() {
	t := m.cur()
	if t == nil {
		return
	}
	line, col, ok := t.buf.LastEdit()
	if !ok {
		m.msg = m.t("msg.no_last_edit")
		return
	}
	if line == t.buf.CurLine() && col == t.buf.Col() {
		m.msg = m.t("msg.at_last_edit")
		return
	}
	m.pushJump()
	t.buf.ClearCursors()
	t.buf.SetCursor(line, col)
	m.clampScroll()
	// SetCursor clamps, so a file that shrank since the change lands on what is
	// left of it: name the line the caret reached, not the one it asked for.
	m.msg = m.t("msg.last_edit", t.buf.CurLine()+1)
}
