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
//
// A pass also waits for the typing to settle: while the user keeps typing,
// the buffer changes every keystroke and a pass would only produce a result
// that is outdated by the time it lands. The wait is restarted as long as
// the text keeps moving, so a long typing burst costs no tokenizing at all.

// syntaxPassDelay is how long the editor waits for the buffer to settle
// before it starts a pass.
const syntaxPassDelay = 150 * time.Millisecond

// syntaxReq asks for a highlighting pass of one tab's text.
type syntaxReq struct {
	tabIdx int
	path   string
	text   string
	syn    *syntax.Highlighter
}

// syntaxPassMsg asks the UI thread to start a highlighting pass: the text
// snapshot is taken on the UI thread, so the buffer is never read from a
// background goroutine.
type syntaxPassMsg struct {
	tabIdx int
}

// syntaxReadyMsg carries a finished highlighting pass.
type syntaxReadyMsg struct {
	tabIdx int
	text   string
	lines  []syntax.HighlightedLine
}

// waitForSyntax waits for the next message of the highlighting pipeline.
// Like the other waiters of the editor it re-arms itself: one waiter is
// running for the whole session.
func waitForSyntax(ch chan tea.Msg) tea.Cmd {
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
		// Styles of a different line count would end up on the wrong lines:
		// drop them now and let the editor paint plain text until the new
		// pass lands.
		if len(t.syntaxCached) != t.buf.LineCount() {
			t.syntaxCached = nil
			t.syntaxText = ""
		}
		t.syntaxPending = true
		t.syntaxWant = text
		m.scheduleSyntax(idx, text)
	}
}

// scheduleSyntax waits for the buffer to settle and then asks the UI thread
// to start the pass.
func (m *Model) scheduleSyntax(tabIdx int, text string) {
	ch := m.syntaxCh
	go func() {
		defer debug.CapturePanicReport(func() {})
		time.Sleep(syntaxPassDelay)
		ch <- syntaxPassMsg{tabIdx: tabIdx}
	}()
}

// startSyntax runs a highlighting pass in the background and hands the
// result to the editor through the syntax channel.
func (m *Model) startSyntax(req syntaxReq) {
	ch := m.syntaxCh
	go func() {
		defer debug.CapturePanicReport(func() {})
		lines := req.syn.HighlightBuffer(req.path, req.text)
		ch <- syntaxReadyMsg{tabIdx: req.tabIdx, text: req.text, lines: lines}
	}()
}

// runSyntax starts the highlighting pass when the text the wait was started
// for is still the text of the buffer. While the user keeps typing the wait
// is restarted instead, so a typing burst costs no tokenizing at all.
func (m *Model) runSyntax(tabIdx int) tea.Cmd {
	if tabIdx < 0 || tabIdx >= len(m.tabs) {
		return nil
	}
	t := &m.tabs[tabIdx]
	text := t.buf.Text()
	if text != t.syntaxWant {
		// The buffer moved on: wait for it to settle again.
		t.syntaxWant = text
		m.scheduleSyntax(tabIdx, text)
		return nil
	}
	if t.syntaxCached != nil && t.syntaxText == text {
		t.syntaxPending = false
		return nil
	}
	req := syntaxReq{tabIdx: tabIdx, path: t.path, text: text, syn: m.syn}
	m.startSyntax(req)
	return nil
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
