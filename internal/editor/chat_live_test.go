package editor

import (
	"os"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/ai"
)

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
	for _, r := range "создай файл index.html с набросками будущего сайта" {
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
