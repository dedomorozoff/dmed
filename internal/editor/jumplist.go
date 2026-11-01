package editor

// The jump list: back and forward over the positions the discrete jumps came
// from. Only a jump the user cannot walk back by hand records anything —
// goto-definition (F12 and Ctrl+Click), a Find-in-Project hit, Go-to-Line and the
// debugger's stop line. Arrows, mouse clicks, the in-file find moves and the walks
// that already own a chord (Alt+N bookmarks, Alt+[ / Alt+] hunks, Ctrl+] the
// bracket pair) never push: a chord that can retrace itself needs no history, and
// a history that grows while the user types is a history nobody can read.
//
// The stacks live in the pane, next to the scroll offsets, so each split answers
// for its own navigation; nothing is persisted. Both commands are pure caret and
// focus moves — no edit, no undo step, no dirty flag.

// jumpHistoryLen caps both stacks; the oldest entries drop off the front.
const jumpHistoryLen = 64

// currentJump reads the caret position the way the history stores it.
func (m *Model) currentJump() (jumpPoint, bool) {
	t := m.cur()
	if t == nil {
		return jumpPoint{}, false
	}
	return jumpPoint{path: t.path, line: t.buf.CurLine(), col: t.buf.Col()}, true
}

// pushJump records the position a jump is leaving. Producers call it in the
// handler that moves the caret, not where the request started: the definition
// reply is async, and the caret standing where it lands is the position back has
// to return to. A new jump abandons the forward branch.
func (m *Model) pushJump() {
	p, ok := m.currentJump()
	if !ok {
		return
	}
	c := m.curPane()
	c.forwards = nil
	if len(c.backs) > 0 && c.backs[len(c.backs)-1] == p {
		return // the same spot twice would make a back press do nothing
	}
	c.backs = append(c.backs, p)
	if over := len(c.backs) - jumpHistoryLen; over > 0 {
		c.backs = c.backs[over:]
	}
}

func (m *Model) jumpBack() { m.jumpStep(true) }

func (m *Model) jumpForward() { m.jumpStep(false) }

// jumpStep is both directions in one body: pop an entry that is not where the
// caret already stands, hand the caret's own position to the opposite stack and
// land. An exhausted history is a refusal with the caret untouched.
func (m *Model) jumpStep(backward bool) {
	c := m.curPane()
	from, to := &c.forwards, &c.backs
	none := "msg.jump_forward_none"
	if backward {
		from, to = &c.backs, &c.forwards
		none = "msg.jump_back_none"
	}
	cur, ok := m.currentJump()
	if !ok {
		return
	}
	for len(*from) > 0 && (*from)[len(*from)-1] == cur {
		*from = (*from)[:len(*from)-1] // an entry that lands nowhere is not a step
	}
	if len(*from) == 0 {
		m.msg = m.t(none)
		return
	}
	j := (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	if len(*to) == 0 || (*to)[len(*to)-1] != cur {
		*to = append(*to, cur)
		if over := len(*to) - jumpHistoryLen; over > 0 {
			*to = (*to)[over:]
		}
	}
	m.restoreJump(j)
}

// restoreJump lands on a recorded position. focusOrOpen resolves the path and
// reopens the file when its tab was closed in the meantime — the only honest
// option once that buffer is gone. SetCursor clamps line and column and drops the
// selection, so a file that changed on disk cannot strand the caret, and
// clampScroll unfolds a block the landing line hides inside.
func (m *Model) restoreJump(j jumpPoint) {
	m.focusOrOpen(j.path)
	t := m.cur()
	if t == nil {
		return
	}
	t.buf.ClearCursors()
	t.buf.SetCursor(j.line, j.col)
	m.clampScroll()
	m.msg = ""
}
