package editor

import (
	"context"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/ai"
	"dmed/internal/debug"
)

// GhostOutputMsg delivers a streaming delta for the ghost text suggestion.
// Gen stamps the request that produced the event: a delta arriving after the
// user typed again belongs to a cancelled stream and must not repaint.
type GhostOutputMsg struct {
	Delta string
	Err   error
	Done  bool
	Gen   uint64
}

// ghostWaitMsg is the debounce window expiring: the moment the editor
// considers the typing paused and asks the model for a continuation.
type ghostWaitMsg struct {
	gen uint64
}

// The context budget and the adaptive windows. Ghost is fill-in-middle: the
// lines after the cursor are what the completion has to fit into, so cutting
// the buffer at a blank line keeps the suffix short without dropping the
// block the caret sits in.
const (
	ghostContextBefore = 8
	ghostContextAfter  = 6

	// The window grows with the keystroke interval: a fast typist gets one
	// suggestion when they pause, a slow one gets one per stop — the same
	// rule every ghost engine ships with, tuned so a burst of typing costs
	// the model nothing at all.
	ghostFastGap    = 180 * time.Millisecond
	ghostMedGap     = 400 * time.Millisecond
	ghostFastWindow = 750 * time.Millisecond
	ghostMedWindow  = 450 * time.Millisecond
	ghostSlowWindow = 250 * time.Millisecond
)

// ghostRhythm caps the keystroke log the window is derived from.
const ghostRhythm = 8

// ghostWindow is the debounce for the next request: the median gap of the
// recent keystrokes picks one of three tiers.
func (m *Model) ghostWindow() time.Duration {
	if len(m.ghostStamps) < 3 {
		return ghostMedWindow // no rhythm established yet
	}
	gaps := make([]time.Duration, 0, len(m.ghostStamps)-1)
	for i := 1; i < len(m.ghostStamps); i++ {
		gaps = append(gaps, m.ghostStamps[i].Sub(m.ghostStamps[i-1]))
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i] < gaps[j] })
	median := gaps[len(gaps)/2]
	switch {
	case median < ghostFastGap:
		return ghostFastWindow
	case median < ghostMedGap:
		return ghostMedWindow
	default:
		return ghostSlowWindow
	}
}

// scheduleGhost restarts the debounce clock: the pending window is dropped
// (its gen goes stale), any in-flight stream is cancelled, and a fresh timer
// asks for a suggestion once the typing settles. Every keystroke and every
// Enter lands here, so the model is asked exactly once per pause.
func (m *Model) scheduleGhost() tea.Cmd {
	if m.ai == nil || !m.cfg.AI.GhostAuto {
		return nil
	}
	now := time.Now()
	m.ghostStamps = append(m.ghostStamps, now)
	if n := len(m.ghostStamps); n > ghostRhythm {
		m.ghostStamps = m.ghostStamps[n-ghostRhythm:]
	}
	m.dismissGhost()
	gen := bumpGen(&m.ghostGen)
	return tea.Tick(m.ghostWindow(), func(time.Time) tea.Msg {
		return ghostWaitMsg{gen: gen}
	})
}

// handleGhostWait fires the request the window was counting down for — if
// the clock was not restarted meanwhile.
func (m *Model) handleGhostWait(msg ghostWaitMsg) tea.Cmd {
	if msg.gen != m.ghostGen {
		return nil
	}
	return m.ghostTrigger()
}

// ghostContext splits the buffer at the caret into the FIM prefix and suffix.
func (m *Model) ghostContext() (prefix, suffix string) {
	t := m.activeTab()
	if t == nil {
		return "", ""
	}
	curLine := t.buf.CurLine()
	col := t.buf.Col()
	lineCount := t.buf.LineCount()

	start := max(0, curLine-ghostContextBefore)
	var pre []string
	for i := start; i <= curLine && i < lineCount; i++ {
		line := string(t.buf.LineAt(i))
		if i == curLine && col < len(line) {
			line = line[:col]
		}
		pre = append(pre, line)
	}
	prefix = strings.Join(pre, "\n")

	var post []string
	if curLine < lineCount {
		line := string(t.buf.LineAt(curLine))
		if col < len(line) {
			post = append(post, line[col:])
		}
	}
	for i := curLine + 1; i < lineCount && len(post) < ghostContextAfter+1; i++ {
		line := string(t.buf.LineAt(i))
		if strings.TrimSpace(line) == "" && len(post) > 0 {
			break // the caret's block ends at its first blank line
		}
		post = append(post, line)
	}
	suffix = strings.Join(post, "\n")
	return prefix, suffix
}

// isCodeModel reports whether the chat model is a completion-tuned one the
// FIM prompt is shaped for. Every name on the list is a completion family:
// "coder" alone covers qwen-coder and deepseek-coder, while a chat model of
// the same vendor ("deepseek-chat") must not match it.
func isCodeModel(model string) bool {
	m := strings.ToLower(model)
	for _, hint := range []string{"coder", "codestral", "devstral", "starcoder", "codegemma", "granite-code"} {
		if strings.Contains(m, hint) {
			return true
		}
	}
	return false
}

// ghostMessages builds the request: a fill-in-middle frame for code models
// (prefix / suffix as two turns, the answer continuing where the prefix
// stops), the old prose frame for everything else.
func ghostMessages(prefix, suffix, model string) []ai.Message {
	if isCodeModel(model) {
		sys := "You complete code. Output only the code that continues the PREFIX " +
			"and must fit before the SUFFIX. No explanations, no fences, no repetition " +
			"of the prefix. Stop at the end of the statement or block."
		user := "PREFIX:\n" + prefix
		if suffix != "" {
			user += "\n\nSUFFIX:\n" + suffix
		}
		return []ai.Message{
			{Role: "system", Content: sys},
			{Role: "user", Content: user},
		}
	}
	return []ai.Message{
		{
			Role: "system",
			Content: "You are a code completion assistant. " +
				"Continue the code from where it left off. " +
				"Return ONLY the raw code continuation. No explanations, " +
				"no markdown fences, no comments — just the next lines of code. " +
				"Stop at a natural boundary (end of block, blank line, etc).",
		},
		{
			Role:    "user",
			Content: "Continue this code:\n\n" + prefix,
		},
	}
}

// ghostTrigger starts a ghost text request. Like the chat panel, it launches
// the streaming goroutine immediately and returns a tea.Cmd that reads the
// streamed output; returning the wait command (a tea.Cmd) here is what lets
// bubbletea deliver GhostOutputMsg messages into Update. Every event is
// stamped with the current generation, so a delta from a stream the user has
// typed past cannot repaint the ghost.
func (m *Model) ghostTrigger() tea.Cmd {
	if m.ai == nil {
		return nil
	}

	// Cancel any in-flight ghost stream (e.g. from a newer window) so
	// concurrent requests can't leak goroutines or mix stale deltas into the
	// current suggestion.
	if m.ghostCancel != nil {
		m.ghostCancel()
		m.ghostCancel = nil
	}

	prefix, suffix := m.ghostContext()
	if prefix == "" && suffix == "" {
		return nil
	}
	msgs := ghostMessages(prefix, suffix, m.cfg.AI.Model)

	t := m.activeTab()
	ctx, cancel := context.WithCancel(context.Background())
	m.ghostCancel = cancel

	gen := m.ghostGen
	ch := make(chan chatEvent, 32)
	m.ghostCh = ch
	m.ghostText = ""
	m.ghostLines = nil
	m.ghostRow = t.buf.CurLine()
	m.ghostCol = t.buf.Col()

	aiClient := m.ai
	reqOpts := m.aiRequestOptions()

	go debug.CapturePanicReport(func() {
		defer close(ch)
		err := aiClient.ChatStream(ctx, ai.Request{Messages: msgs, Options: reqOpts}, ai.Handler{
			Delta: func(delta string) {
				select {
				case ch <- chatEvent{delta: delta}:
				case <-ctx.Done():
				}
			},
		})
		ev := chatEvent{done: true}
		if err != nil {
			ev = chatEvent{err: err}
		}
		select {
		case <-ctx.Done():
		case ch <- ev:
		}
	})

	return waitForGhostOutput(ch, gen)
}

// waitForGhostOutput returns a tea.Cmd that reads the next ghost streaming
// event, stamped with the generation of the request it belongs to.
func waitForGhostOutput(ch <-chan chatEvent, gen uint64) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return GhostOutputMsg{Delta: ev.delta, Err: ev.err, Done: ev.done, Gen: gen}
	}
}

// handleGhostOutput processes a streaming delta for ghost text.
func (m *Model) handleGhostOutput(msg GhostOutputMsg) tea.Cmd {
	if msg.Gen != m.ghostGen {
		return nil // a stale stream: the user typed, the window restarted
	}
	switch {
	case msg.Err != nil:
		m.dismissGhost()
		return nil
	case msg.Done:
		m.ghostVisible = len(m.ghostLines) > 0
		return nil
	default:
		if msg.Delta != "" {
			m.ghostText += msg.Delta
			// Split into lines; ignore trailing partial line for cleaner display
			parts := strings.Split(m.ghostText, "\n")
			m.ghostLines = parts
		}
	}
	return waitForGhostOutput(m.ghostCh, msg.Gen)
}

// dismissGhost clears the ghost text state.
func (m *Model) dismissGhost() {
	if m.ghostCancel != nil {
		m.ghostCancel()
		m.ghostCancel = nil
	}
	m.ghostLines = nil
	m.ghostVisible = false
	m.ghostText = ""
	if m.ghostCh != nil {
		// Drain channel to avoid goroutine leak
		go debug.CapturePanicReport(func() {
			for range m.ghostCh {
			}
		})
		m.ghostCh = nil
	}
}

// applyGhost inserts the ghost text into the buffer at the cursor position.
func (m *Model) applyGhost() {
	if len(m.ghostLines) == 0 {
		m.dismissGhost()
		return
	}

	t := m.activeTab()

	// The suggestion was computed for a specific cursor position; if the cursor
	// moved while it streamed, discard the stale suggestion instead of inserting
	// it in the wrong place.
	if t.buf.CurLine() != m.ghostRow || t.buf.Col() != m.ghostCol {
		m.dismissGhost()
		return
	}

	// The first ghost line is a continuation of the current line,
	// so we only insert the part after the cursor.
	firstLine := m.ghostLines[0]
	if len(m.ghostLines) == 1 {
		// Single-line continuation: just insert after cursor
		t.buf.InsertText(firstLine)
	} else {
		// Multi-line: insert first line remainder, then newline + rest
		if firstLine != "" {
			t.buf.InsertText(firstLine)
		}
		for _, line := range m.ghostLines[1:] {
			t.buf.InsertNewline()
			if line != "" {
				t.buf.InsertText(line)
			}
		}
	}
	m.dismissGhost()
}
