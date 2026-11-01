package editor

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/debug"
	"dmed/internal/vcs"
)

// The gutter shows which lines of the buffer differ from HEAD. Computing
// that diff is a whole-file job (the text is diffed line by line against the
// committed version), and it was done on the UI thread: on a file of a few
// thousand lines it costs tens of milliseconds per keystroke.
//
// The diff therefore runs in the background like the syntax highlighting:
// the editor keeps painting the previous result and swaps in the new one
// when it lands. A diff for text the buffer has moved away from is dropped.

// diffPassDelay is how long the editor waits for the buffer to settle before
// it starts a diff pass.
const diffPassDelay = 150 * time.Millisecond

// diffPassMsg asks the UI thread to start a diff pass.
type diffPassMsg struct {
	tabIdx int
}

// diffReadyMsg carries a finished diff pass.
type diffReadyMsg struct {
	tabIdx int
	text   string
	diff   vcs.FileDiff
}

// waitForDiff waits for the next message of the diff pipeline. Like the
// other waiters of the editor it re-arms itself.
func waitForDiff(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

// requestDiff asks for a diff pass of every visible tab whose cached diff no
// longer matches its buffer.
func (m *Model) requestDiff() {
	seen := map[int]bool{}
	for i := range m.panes {
		idx := m.panes[i].tabIdx
		if idx < 0 || idx >= len(m.tabs) || seen[idx] {
			continue
		}
		seen[idx] = true
		t := &m.tabs[idx]
		if t.path == "" || t.diffPending {
			continue
		}
		repo := t.repoFor(m.repo)
		if repo == nil {
			continue
		}
		text := t.buf.Text()
		if t.diffCached.Lines != nil && t.diffText == text {
			continue
		}
		t.diffPending = true
		t.diffWant = text
		m.scheduleDiff(idx, text)
	}
}

// scheduleDiff waits for the buffer to settle and then asks the UI thread to
// start the pass.
func (m *Model) scheduleDiff(tabIdx int, text string) {
	ch := m.diffCh
	go func() {
		defer debug.CapturePanicReport(func() {})
		time.Sleep(diffPassDelay)
		ch <- diffPassMsg{tabIdx: tabIdx}
	}()
}

// runDiff starts the diff pass when the text the wait was started for is
// still the text of the buffer; otherwise the wait is restarted.
func (m *Model) runDiff(tabIdx int, repo *vcs.Repo) {
	if tabIdx < 0 || tabIdx >= len(m.tabs) {
		return
	}
	t := &m.tabs[tabIdx]
	text := t.buf.Text()
	if text != t.diffWant {
		t.diffWant = text
		m.scheduleDiff(tabIdx, text)
		return
	}
	if t.diffCached.Lines != nil && t.diffText == text {
		t.diffPending = false
		return
	}
	path := t.path
	ch := m.diffCh
	go func() {
		defer debug.CapturePanicReport(func() {})
		d := repo.DiffBuffer(path, text)
		ch <- diffReadyMsg{tabIdx: tabIdx, text: text, diff: d}
	}()
}

// applyDiff stores a finished diff. A diff for text the buffer has moved
// away from is dropped; the next update asks for a fresh one.
func (m *Model) applyDiff(msg diffReadyMsg) {
	if msg.tabIdx < 0 || msg.tabIdx >= len(m.tabs) {
		return
	}
	t := &m.tabs[msg.tabIdx]
	t.diffPending = false
	if msg.text != t.buf.Text() {
		return
	}
	t.diffCached = msg.diff
	t.diffText = msg.text
}
