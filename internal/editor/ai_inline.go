package editor

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/ai"
	"dmed/internal/buffer"
	"dmed/internal/vcs"
)

// InlineOutputMsg delivers one event from the streaming inline AI goroutine.
type InlineOutputMsg struct {
	Delta string
	Err   error
	Done  bool
}

func waitForInlineOutput(ch <-chan chatEvent) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return InlineOutputMsg{Delta: ev.delta, Err: ev.err, Done: ev.done}
	}
}

// startInlineRequest opens the inline AI prompt. If text is selected, that text
// is sent to the AI; otherwise the current line is used.
func (m *Model) startInlineRequest() {
	t := m.cur()
	if t.buf.HasSelection() {
		sl, sc, el, ec := t.buf.SelectionRange()
		m.aiInlineOriginal = t.buf.SelectedText()
		m.aiInlineSelStart = [2]int{sl, sc}
		m.aiInlineSelEnd = [2]int{el, ec}
	} else {
		line := t.buf.LineAt(t.buf.CurLine())
		m.aiInlineOriginal = strings.TrimRight(string(line), "\n")
		m.aiInlineSelStart = [2]int{t.buf.CurLine(), 0}
		m.aiInlineSelEnd = [2]int{t.buf.CurLine(), len(line)}
	}
	m.aiInlineOpen = true
	m.aiInlineInput = nil
	m.aiInlineProposal = ""
	m.aiInlineBusy = false
	m.aiReviewMode = false
	m.msg = ""
}

// handleInlineRequest handles keys while the inline prompt is active (before
// the AI has responded).
func (m *Model) handleInlineRequest(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		if m.aiInlineCancel != nil {
			m.aiInlineCancel()
		}
		m.aiInlineOpen = false
		m.aiInlineBusy = false
		m.msg = ""
	case "enter":
		m.submitInlineRequest()
	case "backspace":
		if n := len(m.aiInlineInput); n > 0 {
			m.aiInlineInput = m.aiInlineInput[:n-1]
		}
	default:
		if len(msg.Text) > 0 {
			m.aiInlineInput = append(m.aiInlineInput, []rune(msg.Text)...)
		}
	}
	return nil
}

// submitInlineRequest sends the selected text + instruction to the AI.
func (m *Model) submitInlineRequest() {
	instruction := strings.TrimSpace(string(m.aiInlineInput))
	if instruction == "" || m.aiInlineBusy {
		return
	}
	if m.ai == nil {
		m.ai = ai.NewProvider(ai.Config{
			Type:   ai.ProviderType(m.cfg.AI.Provider),
			URL:    m.cfg.AI.OllamaURL,
			Model:  m.cfg.AI.Model,
			APIKey: m.cfg.AI.APIKey,
		})
	}
	if m.chatModel == "" {
		m.pickChatModel()
		if m.chatModel == "" {
			m.msg = "no model available; check provider config"
			return
		}
	}

	m.aiInlineOpen = false
	m.aiInlineBusy = true
	m.msg = "AI: rewriting..."

	systemPrompt := "You are a code editor assistant. The user provides code and an instruction. " +
		"Return ONLY the modified code that follows the instruction. " +
		"No explanations, no markdown fences, no extra text — just the raw modified code."

	// Add surrounding context lines so the AI understands the local code.
	tb := m.cur().buf
	ctxBefore, ctxAfter := surroundingContext(tb, m.aiInlineSelStart[0], m.aiInlineSelEnd[0])

	userMsg := "Text:\n```\n" + m.aiInlineOriginal + "\n```\n\nInstruction: " + instruction
	if ctxBefore != "" || ctxAfter != "" {
		userMsg += "\n\nSurrounding context:\n"
		if ctxBefore != "" {
			userMsg += "...\n" + ctxBefore + "\n"
		}
		userMsg += "[BEGIN selected/replaced region]"
		if ctxAfter != "" {
			userMsg += "\n" + ctxAfter + "\n..."
		}
	}

	msgs := []ai.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userMsg},
	}

	if m.aiInlineCancel != nil {
		m.aiInlineCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.aiInlineCancel = cancel

	ch := make(chan chatEvent, 64)
	m.aiInlineCh = ch
	go func() {
		defer close(ch)
		err := m.ai.ChatStream(ctx, msgs, func(d string) {
			select {
			case ch <- chatEvent{delta: d}:
			case <-ctx.Done():
			}
		})
		if err != nil {
			select {
			case ch <- chatEvent{err: err}:
			case <-ctx.Done():
			}
		}
	}()
}

// handleInlineOutput processes a streaming delta or completion from the inline
// AI goroutine. Called from the main Update() loop.
func (m *Model) handleInlineOutput(msg InlineOutputMsg) tea.Cmd {
	if msg.Err != nil {
		m.msg = "AI error: " + msg.Err.Error()
		m.aiInlineBusy = false
		m.aiInlineCh = nil
		return nil
	}
	m.aiInlineProposal += msg.Delta
	if msg.Done {
		m.aiInlineBusy = false
		m.aiInlineCh = nil
		m.startInlineReview()
	}
	return waitForInlineOutput(m.aiInlineCh)
}

// startInlineReview computes the side-by-side diff and enters review mode.
func (m *Model) startInlineReview() {
	proposal := strings.TrimSpace(m.aiInlineProposal)
	if proposal == "" {
		m.msg = "AI returned empty response"
		return
	}
	original := m.aiInlineOriginal
	// Ensure trailing newline for consistent diff
	if !strings.HasSuffix(original, "\n") {
		original += "\n"
	}
	if !strings.HasSuffix(proposal, "\n") {
		proposal += "\n"
	}
	m.aiReviewRows = vcs.SideBySide(original, proposal)
	m.aiReviewLeft = strings.Split(strings.TrimRight(original, "\n"), "\n")
	m.aiReviewRight = strings.Split(strings.TrimRight(proposal, "\n"), "\n")
	m.aiReviewOffY = 0
	m.aiReviewOffX = 0
	m.aiReviewMode = true
	m.msg = ""
}

// handleInlineReview handles keys while the AI diff preview is shown.
func (m *Model) handleInlineReview(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "y", "enter":
		m.applyInlineProposal()
		m.aiReviewMode = false
		m.msg = "AI changes applied"
		return nil
	case "n", "esc":
		m.aiReviewMode = false
		m.aiInlineProposal = ""
		m.aiReviewRows = nil
		m.msg = "AI changes discarded"
		return nil
	case "up", "k":
		if m.aiReviewOffY > 0 {
			m.aiReviewOffY--
		}
	case "down", "j":
		if m.aiReviewOffY < len(m.aiReviewRows)-1 {
			m.aiReviewOffY++
		}
	case "pgup":
		m.aiReviewOffY -= m.paneViewHeight(m.activePane) / 2
		if m.aiReviewOffY < 0 {
			m.aiReviewOffY = 0
		}
	case "pgdn":
		m.aiReviewOffY += m.paneViewHeight(m.activePane) / 2
		maxOff := len(m.aiReviewRows) - 1
		if m.aiReviewOffY > maxOff {
			m.aiReviewOffY = maxOff
		}
	case "home", "g":
		m.aiReviewOffY = 0
	case "end", "G":
		m.aiReviewOffY = len(m.aiReviewRows) - 1
		if m.aiReviewOffY < 0 {
			m.aiReviewOffY = 0
		}
	case "left", "h":
		m.aiReviewOffX -= 8
		if m.aiReviewOffX < 0 {
			m.aiReviewOffX = 0
		}
	case "right", "l":
		m.aiReviewOffX += 8
	}
	return nil
}

// applyInlineProposal replaces the original text in the buffer with the AI proposal.
func (m *Model) applyInlineProposal() {
	t := m.cur()
	proposal := strings.TrimSpace(m.aiInlineProposal)

	if t.buf.HasSelection() {
		// Replace the selected range
		sl, sc := m.aiInlineSelStart[0], m.aiInlineSelStart[1]
		el, ec := m.aiInlineSelEnd[0], m.aiInlineSelEnd[1]
		t.buf.SetCursor(sl, sc)
		t.buf.StartSelection()
		t.buf.SetCursor(el, ec)
		t.buf.DeleteSelection()
		t.buf.InsertText(proposal)
	} else {
		// Replace the entire line content
		line := t.buf.CurLine()
		t.buf.SetCursor(line, 0)
		t.buf.LineEndWithSelect()
		t.buf.DeleteSelection()
		t.buf.InsertText(proposal)
	}
	t.syntaxCached = nil
	t.diffText = ""

	// Clean up review state
	m.aiInlineProposal = ""
	m.aiReviewRows = nil
	m.aiReviewLeft = nil
	m.aiReviewRight = nil
}

// surroundingContext returns up to 3 lines before startLine and up to 3 lines
// after endLine for inline AI context. Empty strings mean no context available.
func surroundingContext(b *buffer.Buffer, startLine, endLine int) (before, after string) {
	const ctx = 3
	var pre []string
	for i := startLine - ctx; i < startLine; i++ {
		if i >= 0 && i < b.LineCount() {
			pre = append(pre, string(b.LineAt(i)))
		}
	}
	var post []string
	for i := endLine + 1; i <= endLine+ctx; i++ {
		if i < b.LineCount() {
			post = append(post, string(b.LineAt(i)))
		}
	}
	return strings.Join(pre, "\n"), strings.Join(post, "\n")
}
