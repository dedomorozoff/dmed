package editor

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/ai"
	"dmed/internal/vcs"
)

// FixOutputMsg delivers one event from the streaming LSP-fix AI goroutine.
type FixOutputMsg struct {
	Delta string
	Err   error
	Done  bool
}

func waitForFixOutput(ch <-chan chatEvent) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return FixOutputMsg{Done: true}
		}
		return FixOutputMsg{Delta: ev.delta, Err: ev.err, Done: ev.done}
	}
}

// startFixRequest opens the AI fix prompt for the current file. The user types
// an instruction (or just presses Enter to fix all diagnostics); the whole file
// is then sent to the AI, which is expected to return the corrected full file.
func (m *Model) startFixRequest() {
	t := m.cur()
	if t == nil || t.path == "" {
		m.msg = "no file open"
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

	m.aiFixOpen = true
	m.aiFixBusy = false
	m.aiFixInput = nil
	m.aiFixProposal = ""
	m.aiFixReviewMode = false
	m.aiFixOriginal = t.buf.Text()
	m.msg = m.t("ai.fix_instr")
}

// handleFixRequest handles keys while the fix prompt is active (before the AI
// has responded).
func (m *Model) handleFixRequest(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.aiFixOpen = false
		m.aiFixBusy = false
		m.aiFixInput = nil
		m.msg = ""
		return nil
	case "backspace":
		if n := len(m.aiFixInput); n > 0 {
			m.aiFixInput = m.aiFixInput[:n-1]
		}
	case "ctrl+u":
		m.aiFixInput = nil
	case "enter":
		return m.submitFixRequest()
	default:
		if len(msg.Text) > 0 {
			m.aiFixInput = append(m.aiFixInput, []rune(msg.Text)...)
		}
	}
	return nil
}

// accumulates the corrected full-file content. The model is instructed to
// return only the fixed file contents.
func (m *Model) submitFixRequest() tea.Cmd {
	instruction := strings.TrimSpace(string(m.aiFixInput))
	t := m.cur()
	if t == nil || m.ai == nil || m.chatModel == "" || m.aiFixBusy {
		return nil
	}
	m.aiFixInput = nil
	m.aiFixBusy = true
	m.msg = m.t("ai.fix_streaming")

	systemPrompt := "You are a code editor assistant. Fix the errors or apply " +
		"the requested change to the file shown and return ONLY the complete " +
		"corrected file contents. Do NOT include explanations, markdown fences, " +
		"or extra text — just the raw file content."
	if instruction != "" {
		systemPrompt += "\n\nInstruction: " + instruction
	}

	if m.aiFixCancel != nil {
		m.aiFixCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.aiFixCancel = cancel

	ch := make(chan chatEvent, 64)
	m.aiFixCh = ch
	go func() {
		defer close(ch)
		msgs := m.fixRequestMessages(t.path, m.aiFixOriginal, systemPrompt)
		err := m.ai.ChatStream(ctx, ai.Request{Messages: msgs, Options: m.aiRequestOptions()}, ai.Handler{
			Delta: func(d string) {
				select {
				case ch <- chatEvent{delta: d}:
				case <-ctx.Done():
				}
			},
		})
		if err != nil {
			select {
			case ch <- chatEvent{err: err}:
			case <-ctx.Done():
			}
		}
	}()
	return waitForFixOutput(ch)
}

// fixRequestMessages builds the request for an AI file fix, including any LSP
// diagnostics currently held for the file as extra context so the model knows
// what to fix.
func (m *Model) fixRequestMessages(path, original, systemPrompt string) []ai.Message {
	var diagText string
	if m.lspClient != nil {
		if diags := m.lspClient.GetDiagnostics(path); len(diags) > 0 {
			diagText = "\n\nDiagnostics:\n"
			for _, d := range diags {
				diagText += fmt.Sprintf("- line %d col %d [%d]: %s\n", d.Line+1, d.Col+1, d.Severity, d.Message)
			}
		}
	}
	return []ai.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: "File: " + path + "\n```\n" + original + "\n```" + diagText},
	}
}

// handleFixOutput processes a streaming delta or completion from the AI fix
// goroutine. Called from the main Update() loop.
func (m *Model) handleFixOutput(msg FixOutputMsg) tea.Cmd {
	if m.aiFixCh == nil {
		return nil
	}
	if msg.Err != nil {
		m.msg = "AI error: " + msg.Err.Error()
		m.aiFixBusy = false
		m.aiFixCh = nil
		return nil
	}
	m.aiFixProposal += msg.Delta
	if msg.Done {
		m.aiFixBusy = false
		m.aiFixCh = nil
		m.startFixReview()
		return nil
	}
	return waitForFixOutput(m.aiFixCh)
}

// cancelFixRequest aborts a streaming AI fix request.
func (m *Model) cancelFixRequest() {
	if m.aiFixCancel != nil {
		m.aiFixCancel()
		m.aiFixCancel = nil
	}
	m.aiFixBusy = false
	m.aiFixCh = nil
	m.aiFixProposal = ""
	m.aiFixOpen = false
	m.msg = m.t("ai.fix_cancelled")
}

// startFixReview computes the side-by-side diff between the original file and
// the AI proposal and enters review mode.
func (m *Model) startFixReview() {
	proposal := strings.TrimSpace(m.aiFixProposal)
	if proposal == "" {
		m.aiFixReviewMode = false
		m.msg = m.t("ai.fix_empty")
		return
	}
	original := m.aiFixOriginal
	if !strings.HasSuffix(original, "\n") && original != "" {
		original += "\n"
	}
	if !strings.HasSuffix(proposal, "\n") {
		proposal += "\n"
	}
	m.aiFixReviewRows = vcs.SideBySide(original, proposal)
	m.aiFixReviewLeft = strings.Split(strings.TrimRight(original, "\n"), "\n")
	m.aiFixReviewRight = strings.Split(strings.TrimRight(proposal, "\n"), "\n")
	m.aiFixReviewOffY = 0
	m.aiFixReviewOffX = 0
	m.aiFixReviewMode = true
	m.msg = ""
}

// handleFixReview handles keys while the AI fix diff is shown.
func (m *Model) handleFixReview(msg tea.KeyPressMsg) tea.Cmd {
	switch gitKeyName(msg) {
	case "y", "enter":
		m.applyFixProposal()
		m.aiFixReviewMode = false
		m.msg = m.t("ai.fix_applied")
		return nil
	case "n", "esc", "r":
		m.aiFixProposal = ""
		m.aiFixReviewRows = nil
		m.aiFixReviewLeft = nil
		m.aiFixReviewRight = nil
		m.aiFixReviewMode = false
		m.msg = m.t("ai.fix_discarded")
		return nil
	case "up", "k":
		if m.aiFixReviewOffY > 0 {
			m.aiFixReviewOffY--
		}
	case "down", "j":
		if m.aiFixReviewOffY < len(m.aiFixReviewRows)-1 {
			m.aiFixReviewOffY++
		}
	case "pgup":
		m.aiFixReviewOffY -= m.paneViewHeight(m.activePane) / 2
		if m.aiFixReviewOffY < 0 {
			m.aiFixReviewOffY = 0
		}
	case "pgdn":
		m.aiFixReviewOffY += m.paneViewHeight(m.activePane) / 2
		if maxOff := len(m.aiFixReviewRows) - 1; m.aiFixReviewOffY > maxOff {
			m.aiFixReviewOffY = maxOff
		}
	case "home", "g":
		m.aiFixReviewOffY = 0
	case "end", "G":
		m.aiFixReviewOffY = len(m.aiFixReviewRows) - 1
		if m.aiFixReviewOffY < 0 {
			m.aiFixReviewOffY = 0
		}
	case "left", "h":
		m.aiFixReviewOffX -= 8
		if m.aiFixReviewOffX < 0 {
			m.aiFixReviewOffX = 0
		}
	case "right", "l":
		m.aiFixReviewOffX += 8
	}
	return nil
}

// applyFixProposal replaces the whole buffer contents with the AI proposal,
// computed against the file text captured when the request started.
func (m *Model) applyFixProposal() {
	t := m.cur()
	if t == nil {
		return
	}
	proposal := strings.TrimRight(m.aiFixProposal, "\n") + "\n"
	b := t.buf
	b.SetCursor(0, 0)
	b.StartSelection()
	for i := 1; i < b.LineCount(); i++ {
		b.MoveDownWithSelect()
	}
	b.LineEndWithSelect()
	b.InsertText(proposal)
	t.syntaxCached = nil
	t.diffText = ""
	m.aiFixProposal = ""
}
