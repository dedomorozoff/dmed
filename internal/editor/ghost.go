package editor

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/ai"
)

// GhostOutputMsg delivers a streaming delta for the ghost text suggestion.
type GhostOutputMsg struct {
	Delta string
	Err   error
	Done  bool
}

// ghostEvent is the internal channel type for ghost streaming.
type ghostEvent = chatEvent

// ghostTrigger starts a ghost text request. Like the chat panel, it launches
// the streaming goroutine immediately and returns a tea.Cmd that reads the
// streamed output; returning the wait command (a tea.Cmd) here is what lets
// bubbletea deliver GhostOutputMsg messages into Update.
func (m *Model) ghostTrigger() tea.Cmd {
	if m.ai == nil {
		return nil
	}

	t := m.activeTab()
	curLine := t.buf.CurLine()
	col := t.buf.Col()

	// Build context: up to 5 lines before cursor + cursor line up to cursor position
	startLine := curLine - 5
	if startLine < 0 {
		startLine = 0
	}

	var ctxLines []string
	for i := startLine; i <= curLine && i < t.buf.LineCount(); i++ {
		line := string(t.buf.LineAt(i))
		if i == curLine {
			// Truncate cursor line at cursor position
			if col < len(line) {
				line = line[:col]
			}
		}
		ctxLines = append(ctxLines, line)
	}
	ctxText := strings.Join(ctxLines, "\n")

	msgs := []ai.Message{
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
			Content: "Continue this code:\n\n" + ctxText,
		},
	}

	ch := make(chan chatEvent, 32)
	m.ghostCh = ch
	m.ghostText = ""
	m.ghostLines = nil
	m.ghostRow = curLine
	m.ghostCol = col

	go func() {
		defer close(ch)
		err := m.ai.ChatStream(context.Background(), msgs, func(delta string) {
			ch <- chatEvent{delta: delta}
		})
		if err != nil {
			ch <- chatEvent{err: err}
		} else {
			ch <- chatEvent{done: true}
		}
	}()

	return waitForGhostOutput(ch)
}

// waitForGhostOutput returns a tea.Cmd that reads the next ghost streaming event.
func waitForGhostOutput(ch <-chan chatEvent) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return GhostOutputMsg{Delta: ev.delta, Err: ev.err, Done: ev.done}
	}
}

// handleGhostOutput processes a streaming delta for ghost text.
func (m *Model) handleGhostOutput(msg GhostOutputMsg) tea.Cmd {
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
	return waitForGhostOutput(m.ghostCh)
}

// dismissGhost clears the ghost text state.
func (m *Model) dismissGhost() {
	m.ghostLines = nil
	m.ghostVisible = false
	m.ghostText = ""
	if m.ghostCh != nil {
		// Drain channel to avoid goroutine leak
		go func() {
			for range m.ghostCh {
			}
		}()
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
