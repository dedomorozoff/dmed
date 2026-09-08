package editor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/ai"
	"dmed/internal/buffer"
)

func TestWrapRunes(t *testing.T) {
	got := wrapRunes("hello brave world", 11)
	want := []string{"hello brave", "world"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestWrapRunesLongWord(t *testing.T) {
	got := wrapRunes("abcdefghij", 4)
	want := []string{"abcd", "efgh", "ij"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestWrapRunesMultilineAndEmpty(t *testing.T) {
	got := wrapRunes("one\ntwo three\n\nfour", 20)
	if len(got) != 4 || got[0] != "one" || got[1] != "two three" || got[2] != "" || got[3] != "four" {
		t.Fatalf("got %v", got)
	}
}

func TestWrapRunesTinyWidth(t *testing.T) {
	got := wrapRunes("ab cd", 1)
	want := []string{"a", "b", "c", "d"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestChatPanelWidthBounds(t *testing.T) {
	m := Model{width: 40}
	if w := m.chatPanelWidth(); w != 24 { // min clamp
		t.Fatalf("narrow: %d", w)
	}
	m = Model{width: 300}
	if w := m.chatPanelWidth(); w != 48 { // max clamp
		t.Fatalf("wide: %d", w)
	}
	m = Model{width: 100}
	if w := m.chatPanelWidth(); w != 40 {
		t.Fatalf("mid: %d", w)
	}
}

func newChatModel() Model {
	m := New()
	m.width = 100
	m.height = 30
	return m
}

func TestToggleChatOpensWithFocusAndCloses(t *testing.T) {
	m := newChatModel()
	m.toggleChat()
	if !m.chatOpen || !m.chatFocus {
		t.Fatal("chat should be open and focused")
	}
	if w := m.rightRailWidth(); w != m.chatPanelWidth() {
		t.Fatalf("rail width = %d", w)
	}
	msg := tea.KeyPressMsg{Code: tea.KeyEscape}
	m.handleKey(msg)
	if m.chatOpen || m.chatFocus {
		t.Fatal("esc should close the panel")
	}
	if w := m.rightRailWidth(); w != 0 {
		t.Fatalf("closed rail width = %d", w)
	}
}

func TestHandleChatTypingAndSubmitGuard(t *testing.T) {
	m := newChatModel()
	m.toggleChat()
	for _, r := range "hi there" {
		m.handleChat(tea.KeyPressMsg{Text: string(r)})
	}
	if string(m.chatIn) != "hi there" {
		t.Fatalf("input = %q", string(m.chatIn))
	}
	m.chatBusy = true // stream running: enter must not submit
	m.chatIn = []rune("second")
	m.handleChat(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.chatMsgs) != 0 || m.chatIn == nil {
		t.Fatalf("busy submit must be ignored; msgs=%d input=%v", len(m.chatMsgs), m.chatIn)
	}
	m.chatBusy = false
	m.chatModel = "test-model" // pretend ollama resolved a model
	m.handleChat(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.chatMsgs) != 1 || m.chatMsgs[0].Role != "user" || m.chatMsgs[0].Content != "second" {
		t.Fatalf("msgs = %+v", m.chatMsgs)
	}
	if !m.chatBusy || len(m.chatRows) == 0 {
		t.Fatal("submit should start streaming state")
	}
}

func TestRebuildChatRowsEmptyStateAndTurns(t *testing.T) {
	m := newChatModel()
	m.width = 100
	m.rebuildChatRows()
	if len(m.chatRows) == 0 {
		t.Fatal("want welcome hint rows")
	}
	m.chatMsgs = nil
	m.chatReply = "short answer"
	m.chatErr = ""
	m.chatBusy = true
	m.rebuildChatRows()
	kinds := make([]string, 0, len(m.chatRows))
	for _, r := range m.chatRows {
		kinds = append(kinds, r.kind)
	}
	hasLabel := false
	for _, k := range kinds {
		if k == "label-ai" {
			hasLabel = true
		}
	}
	if !hasLabel {
		t.Fatalf("want ai label among %v", kinds)
	}
}

func TestComposeChatRailNoOpWhenClosed(t *testing.T) {
	m := newChatModel()
	in := []string{"abc"}
	out := m.composeChatRail(in)
	if len(out) != 1 || out[0] != "abc" {
		t.Fatalf("expected unchanged rows, got %v", out)
	}
}

func TestEditorAreaWidthShrinksForChat(t *testing.T) {
	m := newChatModel() // width 100, no sidebar
	before := m.editorAreaWidth()
	m.toggleChat()
	after := m.editorAreaWidth()
	if after >= before {
		t.Fatalf("editor width should shrink: before=%d after=%d", before, after)
	}
	if before-after != m.rightRailWidth()+0 && before-after != m.chatPanelWidth() {
		t.Fatalf("unexpected shrink delta %d (panel %d)", before-after, m.chatPanelWidth())
	}
}

func TestCancelChatNilSafe(t *testing.T) {
	m := newChatModel()
	m.cancelChat() // must not panic
}

// TestChatNewThreadClearsConversation verifies Ctrl+U fully resets the
// conversation state (messages, partial reply, error, busy flag, tool round,
// input) and cancels a running stream.
func TestChatNewThreadClearsConversation(t *testing.T) {
	m := newChatModel()
	m.toggleChat()
	m.chatModel = "test-model"
	m.chatMsgs = []ai.Message{{Role: "user", Content: "old"}}
	m.chatReply = "partial"
	m.chatErr = "boom"
	m.chatBusy = true
	m.chatToolRound = 3
	m.chatIn = []rune("pending")
	canceled := false
	m.chatCancel = func() { canceled = true }

	m.handleChat(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})

	if m.chatBusy || len(m.chatMsgs) != 0 || m.chatReply != "" || m.chatErr != "" || m.chatToolRound != 0 || m.chatIn != nil {
		t.Fatalf("thread must be fully reset: busy=%v msgs=%d reply=%q err=%q round=%d in=%v",
			m.chatBusy, len(m.chatMsgs), m.chatReply, m.chatErr, m.chatToolRound, m.chatIn)
	}
	if !canceled {
		t.Fatal("a running stream must be cancelled")
	}
	if len(m.chatRows) == 0 {
		t.Fatal("cleared thread must still show the welcome/empty hints")
	}
}

// TestChatPanelShowsHintBar verifies the chat panel reserves a bottom line that
// tells the user how to start a new thread and close the panel.
func TestChatPanelShowsHintBar(t *testing.T) {
	m := newChatModel()
	m.toggleChat()
	if !strings.Contains(m.View().Content, "Ctrl+U new thread") {
		t.Fatalf("chat panel must render the key hint, got:\n%s", m.View().Content)
	}
}

// TestChatSubmitReturnsStreamCmd verifies chatSubmit only starts a stream (and
// returns a cmd to read it) when there is input and no stream is running.
// Returning a cmd for the running stream's channel on a busy submit used to
// spawn a second consumer that garbled deltas and lost the terminal event.
func TestChatSubmitReturnsStreamCmdOnlyWhenStarting(t *testing.T) {
	m := newChatModel()
	m.toggleChat()
	m.chatModel = "test-model"
	m.ai = &fakeProvider{}

	if cmd := m.chatSubmit(); cmd != nil {
		t.Fatal("empty submit should not start a stream")
	}
	m.chatIn = []rune("hello")
	m.chatBusy = true
	if cmd := m.chatSubmit(); cmd != nil {
		t.Fatal("busy submit should not start a stream")
	}
	m.chatBusy = false
	if cmd := m.chatSubmit(); cmd == nil {
		t.Fatal("ready submit should return a stream cmd")
	}
}

// TestInlineSubmitReturnsStreamCmd verifies inline rewrite submits return the
// wait command. Previously the cmd was dropped, so the stream goroutine filled
// its channel unread and the rewrite hung at "AI: rewriting...".
func TestInlineSubmitReturnsStreamCmd(t *testing.T) {
	m := New()
	m.width = 100
	m.height = 30
	m.tabs = []tab{{buf: buffer.Load("func foo() {\n}\n")}}
	m.initPanes()
	m.ai = &fakeProvider{}
	m.chatModel = "test-model"
	m.startInlineRequest()
	m.aiInlineInput = []rune("rename")
	if cmd := m.submitInlineRequest(); cmd == nil {
		t.Fatal("inline submit should return a stream cmd")
	}
}
