package editor

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/debug"
	"dmed/internal/syntax"
)

// Syntax highlighting is a whole-file job: chroma re-tokenizes the entire
// buffer, which on a large file costs seconds and a lot of garbage. Doing
// that on the UI thread made every keystroke wait for it.
//
// The pass therefore runs in the background. The editor keeps painting the
// previous result while the new one is computed and swaps it in when it
// arrives; a pass whose text is already outdated is dropped instead of
// painted.

// syntaxPassDelay is how long a pass waits before it starts. It keeps a
// burst of typing from queueing a highlighting run per keystroke.
const syntaxPassDelay = 80 * time.Millisecond

// syntaxReq asks for a highlighting pass of one tab's text.
type syntaxReq struct {
	tabIdx int
	path   string
	text   string
	syn    *syntax.Highlighter
	delay  bool // wait before the pass: an outdated highlight is on screen
}

// syntaxReadyMsg carries a finished highlighting pass.
type syntaxReadyMsg struct {
	tabIdx int
	text   string
	lines  []syntax.HighlightedLine
}

// waitForSyntax waits for the next finished highlighting pass. Like the
// other waiters of the editor it re-arms itself: one waiter is running for
// the whole session.
func waitForSyntax(ch chan syntaxReadyMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

// requestSyntax asks for a highlighting pass of every visible tab whose
// cached highlight no longer matches its buffer. At most one pass per tab is
// in flight, so typing never piles up work.
func (m *Model) requestSyntax() {
	seen := map[int]bool{}
	for i := range m.panes {
		idx := m.panes[i].tabIdx
		if idx < 0 || idx >= len(m.tabs) || seen[idx] {
			continue
		}
		seen[idx] = true
		t := &m.tabs[idx]
		if t.syntaxPending {
			continue
		}
		text := t.buf.Text()
		if t.syntaxCached != nil && t.syntaxText == text {
			continue
		}
		// The first pass of a file has nothing to fall back on: it starts
		// right away. Later passes wait, so that an outdated highlight stays
		// on screen while the user is typing.
		req := syntaxReq{tabIdx: idx, path: t.path, text: text, syn: m.syn, delay: t.syntaxCached != nil}
		// Styles of a different line count would end up on the wrong lines:
		// drop them and let the editor paint plain text until the new pass
		// lands.
		if len(t.syntaxCached) != t.buf.LineCount() {
			t.syntaxCached = nil
			t.syntaxText = ""
		}
		t.syntaxPending = true
		m.startSyntax(req)
	}
}

// startSyntax runs a highlighting pass in the background and hands the
// result to the editor through the syntax channel.
func (m *Model) startSyntax(req syntaxReq) {
	ch := m.syntaxCh
	go func() {
		defer debug.CapturePanicReport(func() {})
		if req.delay {
			// Let a burst of edits settle before the expensive part starts.
			time.Sleep(syntaxPassDelay)
		}
		lines := req.syn.HighlightBuffer(req.path, req.text)
		ch <- syntaxReadyMsg{tabIdx: req.tabIdx, text: req.text, lines: lines}
	}()
}

// applySyntax stores a finished pass. A pass for text the buffer has moved
// away from is dropped; the next update asks for a fresh one.
func (m *Model) applySyntax(msg syntaxReadyMsg) {
	if msg.tabIdx < 0 || msg.tabIdx >= len(m.tabs) {
		return
	}
	t := &m.tabs[msg.tabIdx]
	t.syntaxPending = false
	if msg.text != t.buf.Text() {
		return
	}
	t.syntaxCached = msg.lines
	t.syntaxText = msg.text
}
