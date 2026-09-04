package editor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"dmed/internal/ai"
	"dmed/internal/buffer"
)

// Right-side AI chat panel backed by a local Ollama server (Alt+A).
// While the panel is open it owns keyboard focus; Esc closes it. A running
// stream keeps appending while the panel is hidden and is cancelled on quit.

var (
	chatUserLabelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true)
	chatAILabelStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("176")).Bold(true)
	chatUserTextStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	chatAITextStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	chatToolLabelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	chatToolTextStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("222"))
)

type chatRow struct {
	kind string // "label-you" | "user" | "label-ai" | "ai" | "tool" | "hint" | "err"
	text string
}

type chatEvent struct {
	delta string
	err   error
	done  bool
}

// ChatOutputMsg delivers one event from the streaming chat goroutine.
type ChatOutputMsg struct {
	Delta string
	Err   error
	Done  bool
}

func waitForChatOutput(ch <-chan chatEvent) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return ChatOutputMsg{Done: true}
		}
		return ChatOutputMsg{Delta: ev.delta, Err: ev.err, Done: ev.done}
	}
}

func (m Model) chatPanelWidth() int {
	pct := m.cfg.UI.ChatWidthPct
	if pct <= 0 {
		pct = 40
	}
	w := m.width * pct / 100
	if w < 24 {
		w = 24
	}
	if w > 48 {
		w = 48
	}
	return w
}

func (m Model) chatInnerWidth() int {
	w := m.chatPanelWidth() - 3
	if w < 1 {
		w = 1
	}
	return w
}

func (m Model) rightRailWidth() int {
	if m.chatOpen {
		return m.chatPanelWidth()
	}
	return 0
}

func (m *Model) toggleChat() tea.Cmd {
	m.chatOpen = !m.chatOpen
	m.chatFocus = m.chatOpen
	m.msg = ""
	if m.ai == nil {
		m.ai = ai.NewProvider(ai.Config{
			Type:   ai.ProviderType(m.cfg.AI.Provider),
			URL:    m.cfg.AI.OllamaURL,
			Model:  m.cfg.AI.Model,
			APIKey: m.cfg.AI.APIKey,
		})
	}
	if m.chatOpen && m.chatModel == "" {
		m.pickChatModel()
	}
	return nil
}

// pickChatModel resolves the model once: DMED_MODEL wins, otherwise the
// first model reported by the server.
func (m *Model) pickChatModel() {
	if m.cfg.AI.Model != "" {
		m.chatModel = m.cfg.AI.Model
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	models, err := m.ai.Models(ctx)
	switch {
	case err != nil:
		m.msg = "provider offline (" + err.Error() + ")"
	case len(models) == 0:
		m.msg = "no models available"
	default:
		m.chatModel = models[0]
		m.ai = ai.NewProvider(ai.Config{
			Type:   ai.ProviderType(m.cfg.AI.Provider),
			URL:    m.cfg.AI.OllamaURL,
			Model:  m.chatModel,
			APIKey: m.cfg.AI.APIKey,
		})
	}
}

func (m *Model) handleChat(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.chatOpen = false
		m.chatFocus = false
		m.msg = ""
	case "enter":
		m.chatSubmit()
		return waitForChatOutput(m.chatCh)
	case "backspace":
		if n := len(m.chatIn); n > 0 {
			m.chatIn = m.chatIn[:n-1]
		}
	case "pgup":
		m.chatScroll += m.paneViewHeight(m.activePane) / 2
	case "pgdn":
		m.chatScroll -= m.paneViewHeight(m.activePane) / 2
		if m.chatScroll < 0 {
			m.chatScroll = 0
		}
	case "ctrl+u": // clear conversation
		m.chatMsgs = nil
		m.chatReply = ""
		m.chatErr = ""
		m.chatBusy = false
		m.chatScroll = 0
	default:
		if len(msg.Text) > 0 {
			m.chatIn = append(m.chatIn, []rune(msg.Text)...)
		}
	}
	return nil
}

func (m *Model) chatSubmit() {
	text := strings.TrimSpace(string(m.chatIn))
	if text == "" || m.chatBusy {
		return // keep the input when the request cannot go out
	}
	m.chatIn = nil
	if m.chatModel == "" {
		m.pickChatModel()
		if m.chatModel == "" {
			m.chatErr = "no model available; start ollama or run: ollama pull llama3.2"
			m.rebuildChatRows()
			return
		}
	}

	m.chatMsgs = append(m.chatMsgs, ai.Message{Role: "user", Content: text})
	m.chatReply = ""
	m.chatErr = ""
	m.chatBusy = true
	m.chatScroll = 0
	m.chatToolRound = 0
	m.rebuildChatRows()

	msgs := m.chatRequestMessages()

	if m.chatCancel != nil {
		m.chatCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.chatCancel = cancel

	ch := make(chan chatEvent, 64)
	m.chatCh = ch
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

// chatRequestMessages snapshots the conversation with a system prompt that
// carries the active file (or selection) as context.
func (m *Model) chatRequestMessages() []ai.Message {
	var b strings.Builder
	b.WriteString(m.cfg.AI.SystemPrompt)
	b.WriteString(toolSystemPrompt)
	if t := m.cur(); t != nil && t.path != "" {
		b.WriteString("\n\nCurrent file: " + t.path + "\n```")
		content := t.buf.Text()
		truncated := false
		if r := []rune(content); len(r) > m.cfg.AI.ContextMax {
			content = string(r[:m.cfg.AI.ContextMax])
			truncated = true
		}
		b.WriteString(content + "\n```")
		if truncated {
			b.WriteString("\n(file truncated for context)")
		}
		if sel := t.buf.SelectedText(); sel != "" {
			sr := []rune(sel)
			if len(sr) > m.cfg.AI.ContextMax {
				sr = sr[:m.cfg.AI.ContextMax]
			}
			b.WriteString("\n\nSelected text:\n" + string(sr))
		}
	}
	msgs := make([]ai.Message, 0, len(m.chatMsgs)+1)
	msgs = append(msgs, ai.Message{Role: "system", Content: b.String()})
	msgs = append(msgs, m.chatMsgs...)
	return msgs
}

const maxChatToolRounds = 6

// maybeRunChatTools inspects the last assistant message for tool blocks. If
// any are present, it executes them, appends the results to the conversation,
// and returns a tea.Cmd that continues the stream. target is a short label of
// what was executed (for the status bar). If there are no tools, it returns
// ("" , nil).
func (m *Model) maybeRunChatTools() (target string, cmd tea.Cmd) {
	if len(m.chatMsgs) == 0 {
		return "", nil
	}
	last := m.chatMsgs[len(m.chatMsgs)-1]
	if last.Role != "assistant" {
		return "", nil
	}
	tools := parseToolCalls(last.Content)
	if len(tools) == 0 {
		return "", nil
	}
	if m.chatToolRound >= maxChatToolRounds {
		m.chatErr = "tool loop exceeded max iterations"
		m.rebuildChatRows()
		return "", nil
	}
	m.chatToolRound++

	// Append results as a user turn summarizing tool outcomes.
	var res strings.Builder
	for _, tc := range tools {
		target = tc.name
		out := executeTool(m, tc)
		res.WriteString("\n=== TOOL RESULT: " + tc.name)
		if tc.arg != "" {
			res.WriteString(": " + tc.arg)
		}
		res.WriteString(" ===\n" + out + "\n")
	}

	// Edit results also refresh matching open buffers.
	for _, tc := range tools {
		if tc.name != "EDIT" {
			continue
		}
		full := resolvePath(m.root, tc.arg)
		for i := range m.tabs {
			if m.tabs[i].path == "" {
				continue
			}
			abs, _ := filepath.Abs(m.tabs[i].path)
			if abs == full && !m.tabs[i].buf.Dirty() {
				if data, err := os.ReadFile(full); err == nil {
					m.tabs[i].buf = buffer.Load(strings.ReplaceAll(string(data), "\r\n", "\n"))
					m.tabs[i].syntaxCached = nil
					m.tabs[i].diffText = ""
				}
			}
		}
	}

	m.chatMsgs = append(m.chatMsgs, ai.Message{Role: "user", Content: res.String()})

	if m.chatCancel != nil {
		m.chatCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.chatCancel = cancel

	ch := make(chan chatEvent, 64)
	m.chatCh = ch
	go func() {
		defer close(ch)
		err := m.ai.ChatStream(ctx, m.chatRequestMessages(), func(d string) {
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

	return target, func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return ChatOutputMsg{Done: true}
		}
		return ChatOutputMsg{Delta: ev.delta, Err: ev.err, Done: ev.done}
	}
}

func (m *Model) rebuildChatRows() {
	inner := m.chatInnerWidth()
	rows := make([]chatRow, 0, 64)
	add := func(kind, text string) { rows = append(rows, chatRow{kind: kind, text: text}) }

	addTurn := func(label, labelKind, textKind, content string) {
		add(labelKind, label)
		for _, l := range wrapRunes(content, inner) {
			add(textKind, l)
		}
		add("hint", "")
	}

	for _, msg := range m.chatMsgs {
		if msg.Role == "user" {
			if strings.HasPrefix(msg.Content, "=== TOOL RESULT:") {
				addTurn(" tool", "label-tool", "tool", msg.Content)
			} else {
				addTurn(" you", "label-you", "user", msg.Content)
			}
		} else {
			addTurn(" ai", "label-ai", "ai", msg.Content)
		}
	}
	if m.chatBusy && m.chatReply == "" {
		add("hint", " thinking...")
	}
	if m.chatReply != "" {
		addTurn(" ai", "label-ai", "ai", m.chatReply)
	}
	if m.chatErr != "" {
		for _, l := range wrapRunes("[error] "+m.chatErr, inner) {
			add("err", l)
		}
	}
	if len(rows) == 0 {
		add("hint", " Local AI via Ollama: free and offline.")
		add("hint", " Type a question, Enter sends.")
	}
	m.chatRows = rows
}

// cancelChat stops any running stream (called from shutdown).
func (m *Model) cancelChat() {
	if m.chatCancel != nil {
		m.chatCancel()
		m.chatCancel = nil
	}
}

// wrapRunes greedily wraps text to width w, hard-breaking words longer
// than w. Existing newlines are honored.
func wrapRunes(s string, w int) []string {
	if w < 1 {
		w = 1
	}
	var out []string
	for _, para := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if para == "" {
			out = append(out, "")
			continue
		}
		cur := ""
		for _, word := range strings.Split(para, " ") {
			if r := []rune(word); len(r) > w {
				if cur != "" { // flush pending line before hard-breaking
					out = append(out, cur)
					cur = ""
				}
				for len(r) > w {
					out = append(out, string(r[:w]))
					r = r[w:]
				}
				word = string(r)
			}
			switch {
			case cur == "":
				cur = word
			case lipgloss.Width(cur)+1+lipgloss.Width(word) <= w:
				cur += " " + word
			default:
				out = append(out, cur)
				cur = word
			}
		}
		out = append(out, cur)
	}
	return out
}
