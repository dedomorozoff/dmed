package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
	msg := tea.KeyMsg{Type: tea.KeyEscape}
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
		m.handleChat(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if string(m.chatIn) != "hi there" {
		t.Fatalf("input = %q", string(m.chatIn))
	}
	m.chatBusy = true // stream running: enter must not submit
	m.chatIn = []rune("second")
	m.handleChat(tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.chatMsgs) != 0 || m.chatIn == nil {
		t.Fatalf("busy submit must be ignored; msgs=%d input=%v", len(m.chatMsgs), m.chatIn)
	}
	m.chatBusy = false
	m.chatModel = "test-model" // pretend ollama resolved a model
	m.handleChat(tea.KeyMsg{Type: tea.KeyEnter})
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
