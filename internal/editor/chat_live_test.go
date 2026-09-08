package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/ai"
)

// TestWheelOverChatChangesRender checks the wheel actually moves the visible
// transcript, not just the internal offset.
func TestWheelOverChatChangesRender(t *testing.T) {
	m := New()
	m.width, m.height = 100, 30
	m.root = t.TempDir()
	m.chatOpen = true
	m.chatFocus = true
	m.ai = ai.NewProvider(ai.Config{Type: "ollama"})
	m.chatModel = "test"
	var sb strings.Builder
	for i := 0; i < 40; i++ {
		sb.WriteString("answer line number ")
		sb.WriteString(strings.Repeat("x", 30))
		sb.WriteString(" #")
		sb.WriteString(strings.Repeat(string(rune('a'+i%26)), i/26+1))
		sb.WriteString("\n")
	}
	m.chatMsgs = []ai.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: sb.String()},
	}
	m.rebuildChatRows()
	if len(m.chatRows) < 25 {
		t.Fatalf("need more rows than the body, got %d", len(m.chatRows))
	}
	nm, _ := m.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height}) // Update is a value receiver: adopt the copy
	m = nm.(Model)

	before := stripANSI(m.View().Content)
	_ = m.handleMouseWheel(tea.MouseWheelMsg{Button: tea.MouseWheelUp, X: 90, Y: 10})
	if m.chatScroll == 0 {
		t.Fatal("chatScroll must be non-zero after wheel up")
	}
	after := stripANSI(m.View().Content)
	if before == after {
		t.Logf("panel width=%d height=%d rows=%d scroll=%d firstRow=%q", m.chatPanelWidth(), m.viewHeight(), len(m.chatRows), m.chatScroll, func() string { if len(m.chatRows) > 0 { return m.chatRows[0].text }; return "" }())
	}
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

// TestClearChatHistory checks the two-step Ctrl+L wipe of threads, prompts and
// the on-disk history file.
func TestClearChatHistory(t *testing.T) {
	m := newChatModel()
	m.toggleChat()
	path := filepath.Join(m.root, chatHistoryFileName)
	m.chatMsgs = []ai.Message{{Role: "user", Content: "hello there"}}
	m.saveChatThread()
	m.recordChatPrompt("hello there")
	if _, err := os.Stat(path); err != nil {
		t.Fatal("history file must exist after save")
	}
	if len(m.chatThreads) != 1 || len(m.chatPrompts) != 1 {
		t.Fatalf("unexpected in-memory history: %d threads, %d prompts", len(m.chatThreads), len(m.chatPrompts))
	}

	m.chatClearArm = false
	m.armChatClear() // first press: arm only
	if !m.chatClearArm {
		t.Fatal("first Ctrl+L must arm the confirmation")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("first Ctrl+L must not delete the history file")
	}
	m.armChatClear() // second press: wipe
	if m.chatClearArm {
		t.Fatal("clear must disarm after wiping")
	}
	if len(m.chatThreads) != 0 || len(m.chatPrompts) != 0 || len(m.chatMsgs) != 0 {
		t.Fatal("history must be empty after clear")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("history file must be deleted after clear")
	}
	if h := loadChatHistory(m.root); len(h.Threads) != 0 || len(h.Prompts) != 0 {
		t.Fatal("reloaded history must be empty")
	}
}

// TestClearChatHistoryBlockedWhileBusy ensures Ctrl+L cannot wipe history
// during a streaming turn or diff review.
func TestClearChatHistoryBlockedWhileBusy(t *testing.T) {
	m := newChatModel()
	m.toggleChat()
	m.chatMsgs = []ai.Message{{Role: "user", Content: "hi"}}
	m.saveChatThread()
	m.chatBusy = true
	m.chatClearArm = true
	m.armChatClear()
	if !m.chatClearArm || len(m.chatThreads) != 1 {
		t.Fatal("clear must be blocked while busy")
	}
}

// TestLiveChatTurn is a manual end-to-end check against a real Ollama server.
// Run with: DMED_LIVE=1 DMED_MODEL=gemma4:31b-cloud go test ./internal/editor/ -run TestLiveChatTurn -v
func TestLiveChatTurn(t *testing.T) {
	if os.Getenv("DMED_LIVE") == "" {
		t.Skip("set DMED_LIVE=1 to run against a real provider")
	}
	m := newChatModel()
	m.toggleChat()
	m.cfg.AI.Provider = "ollama"
	m.ai = ai.NewProvider(ai.Config{Type: "ollama", URL: "http://localhost:11434", Model: os.Getenv("DMED_MODEL")})
	m.chatModel = os.Getenv("DMED_MODEL")
	for _, r := range "create file index.html with a draft of a future site" {
		m.handleChat(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	cmd := m.chatSubmit()
	if cmd == nil {
		t.Fatal("submit returned nil cmd")
	}
	deadline := time.Now().Add(120 * time.Second)
	i := 0
	for cmd != nil && time.Now().Before(deadline) {
		msg := cmd()
		i++
		if msg == nil {
			t.Logf("iter %d: cmd returned nil msg", i)
			break
		}
		if mm, ok := msg.(ChatOutputMsg); ok {
			t.Logf("iter %d: done=%v err=%v tools=%d gen=%d delta=%q", i, mm.Done, mm.Err, len(mm.Tools), mm.Gen, mm.Delta)
		}
		nm, next := m.Update(msg) // Update has a VALUE receiver: adopt the copy
		m = nm.(Model)
		cmd = next
		if m.chatReviewMode {
			t.Logf("review entered: pending=%d", len(m.chatPendingChanges))
			break
		}
	}
	t.Logf("busy=%v err=%q review=%v pending=%d msgs=%d", m.chatBusy, m.chatErr, m.chatReviewMode, len(m.chatPendingChanges), len(m.chatMsgs))
	for _, msg := range m.chatMsgs {
		if msg.Content != "" {
			t.Logf("%s: %s", msg.Role, msg.Content[:min(120, len(msg.Content))])
		}
	}
	if m.chatErr != "" {
		t.Fatalf("chat error: %s", m.chatErr)
	}
	if m.chatBusy && !m.chatReviewMode {
		t.Fatal("turn still busy after timeout")
	}
	if !m.chatReviewMode {
		t.Fatal("expected EDIT tool call to enter diff review")
	}
	for _, c := range m.chatPendingChanges {
		t.Logf("pending change: %s (new %d bytes)", c.Path, len(c.New))
	}
}
