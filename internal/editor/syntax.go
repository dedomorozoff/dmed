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

// syntaxChunkLines is how many lines a pass publishes at once. The first
// chunk covers the visible part of the file, the rest follows in bigger
// steps — every chunk costs a frame.
const syntaxChunkLines = 200

// syntaxReq asks for a highlighting pass of one tab's text. inc carries the
// tab's incremental cache (nil on the first pass of a tab); incLang is the
// language the cache was built for, so a language change resets it.
type syntaxReq struct {
	tabIdx  int
	path    string
	text    string
	syn     *syntax.Highlighter
	inc     *syntax.Incremental
	incLang string
}

// syntaxPassMsg asks the UI thread to start a highlighting pass: the text
// snapshot is taken on the UI thread, so the buffer is never read from a
// background goroutine.
type syntaxPassMsg struct {
	tabIdx int
}

// syntaxReadyMsg carries a part of a highlighting pass: the lines that were
// tokenized (or spliced) so far, at offset. The last part of a pass is marked
// complete. inc is the cache the pass ran through — it becomes the tab's
// cache for the next pass; incLang stamps the language it was built for.
type syntaxReadyMsg struct {
	tabIdx   int
	text     string
	lines    []syntax.HighlightedLine
	offset   int
	complete bool
	inc      *syntax.Incremental
	incLang  string
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
		if t.syntaxCached != nil && t.syntaxComplete && t.syntaxText == text {
			continue
		}
		// Styles of a different line count would end up on the wrong lines:
		// drop them now and let the editor paint plain text until the new
		// pass lands. A pass that is running is not touched: its lines are
		// the ones of the current text.
		if len(t.syntaxCached) != t.buf.LineCount() {
			t.syntaxCached = nil
			t.syntaxText = ""
			t.syntaxComplete = false
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
// result to the editor through the syntax channel. The pass publishes the
// lines it has tokenized while it runs, so the visible part of a large file
// gets its colors without waiting for the whole file.
//
// The pass goes through the tab's incremental cache: lines the previous pass
// already tokenized in the same lexer state are spliced, only the changed
// region and its re-sync window are tokenized fresh. A cache keyed by a
// different language than the file now has is dropped first.
func (m *Model) startSyntax(req syntaxReq) {
	ch := m.syntaxCh
	go func() {
		defer debug.CapturePanicReport(func() {})
		lang := syntax.Lang(req.path)
		inc := req.inc
		if inc == nil || req.incLang != lang {
			inc = syntax.NewIncremental()
		}
		lines, _ := inc.Update(req.path, req.text, req.syn)
		// Publish in chunks so the visible part paints first.
		for i := 0; i < len(lines); i += syntaxChunkLines {
			end := i + syntaxChunkLines
			if end > len(lines) {
				end = len(lines)
			}
			ch <- syntaxReadyMsg{
				tabIdx:   req.tabIdx,
				text:     req.text,
				lines:    lines[i:end],
				offset:   i,
				complete: end == len(lines),
				inc:      inc,
				incLang:  lang,
			}
		}
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
	req := syntaxReq{
		tabIdx:  tabIdx,
		path:    t.path,
		text:    text,
		syn:     m.syn,
		inc:     t.inc,
		incLang: t.incLang,
	}
	m.startSyntax(req)
	return nil
}

// applySyntax stores a part of a pass. A part for text the buffer has moved
// away from is dropped; the next update asks for a fresh pass. The cache the
// pass ran through is adopted even when the text moved on: it holds the
// lines of the text it tokenized, which the next pass diffs against —
// adopting it is what makes the next pass incremental rather than cold.
func (m *Model) applySyntax(msg syntaxReadyMsg) {
	if msg.tabIdx < 0 || msg.tabIdx >= len(m.tabs) {
		return
	}
	t := &m.tabs[msg.tabIdx]
	if msg.inc != nil && (t.inc == nil || msg.inc != t.inc || t.incLang != msg.incLang) {
		t.inc = msg.inc
		t.incLang = msg.incLang
	}
	if msg.complete {
		t.syntaxPending = false
	}
	if msg.text != t.buf.Text() {
		return
	}
	if t.syntaxText != msg.text {
		// A new pass: the lines of the old one do not fit the text any more.
		// (The incremental cache survives — the pass adopted it above; it
		// holds the previous text's lines for the next diff.)
		t.syntaxCached = nil
		t.syntaxText = msg.text
		t.syntaxComplete = false
	}
	// The chunk lands at its offset: chunks of a pass cover the text in
	// order, and a chunk may splice lines the cache reused (which the buffer
	// below may not have seen yet from earlier chunks of the same pass).
	for len(t.syntaxCached) < msg.offset {
		t.syntaxCached = append(t.syntaxCached, syntax.HighlightedLine{})
	}
	if msg.offset+len(msg.lines) <= len(t.syntaxCached) {
		copy(t.syntaxCached[msg.offset:msg.offset+len(msg.lines)], msg.lines)
	} else {
		t.syntaxCached = append(t.syntaxCached[:msg.offset], msg.lines...)
	}
	if msg.complete {
		t.syntaxComplete = true
	}
}
