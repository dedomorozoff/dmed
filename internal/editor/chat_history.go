package editor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dmed/internal/ai"
)

// Chat history persists recent conversation threads and sent prompts to a
// JSON file next to the project (like the session file), so the AI panel
// survives restarts. Threads are stored newest-first; the current thread is
// addressed by its index in that list (-1 means a brand-new unsaved thread).

const (
	chatHistoryFileName = ".dmed_chat.json"
	maxChatThreads      = 20
	maxChatPrompts      = 100
)

// chatThread is one persisted conversation.
type chatThread struct {
	Title   string       `json:"title"`
	Model   string       `json:"model,omitempty"`
	Msgs    []ai.Message `json:"msgs"`
	Updated time.Time    `json:"updated"`
}

// chatHistory is the on-disk shape of the AI panel history.
type chatHistory struct {
	Threads []chatThread `json:"threads"`
	Prompts []string     `json:"prompts"`
}

// chatHistoryPath returns the history file location for a project root or user home.
func chatHistoryPath(root string) string {
	if root != "" {
		return filepath.Join(root, chatHistoryFileName)
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, chatHistoryFileName)
	}
	return chatHistoryFileName
}

func loadChatHistory(root string) *chatHistory {
	data, err := os.ReadFile(chatHistoryPath(root))
	if err != nil {
		return &chatHistory{}
	}
	var h chatHistory
	if err := json.Unmarshal(data, &h); err != nil {
		return &chatHistory{} // corrupt file: start fresh
	}
	return &h
}

func saveChatHistory(root string, h *chatHistory) {
	if len(h.Threads) > maxChatThreads {
		h.Threads = h.Threads[:maxChatThreads]
	}
	if len(h.Prompts) > maxChatPrompts {
		h.Prompts = h.Prompts[len(h.Prompts)-maxChatPrompts:]
	}
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(chatHistoryPath(root), data, 0o644)
}

// loadChatPanelHistory reads the persisted threads and prompts when the chat
// panel is opened for the first time and selects the newest thread.
func (m *Model) loadChatPanelHistory() {
	h := loadChatHistory(m.root)
	m.chatThreads = h.Threads
	m.chatPrompts = h.Prompts
	if len(m.chatThreads) > 0 {
		m.chatThreadPos = 0
		m.chatMsgs = append([]ai.Message(nil), m.chatThreads[0].Msgs...)
		m.rebuildChatRows()
	}
	m.chatHistLoaded = true
}

// persistChatHistory writes the current threads + prompts to disk.
func (m *Model) persistChatHistory() {
	saveChatHistory(m.root, &chatHistory{Threads: m.chatThreads, Prompts: m.chatPrompts})
}

// saveChatThread stores the current conversation as a thread (updating it in
// place if it already has a slot). No-op while the conversation is empty.
func (m *Model) saveChatThread() {
	if len(m.chatMsgs) == 0 {
		return
	}
	msgs := append([]ai.Message(nil), m.chatMsgs...)
	if m.chatThreadPos >= 0 && m.chatThreadPos < len(m.chatThreads) {
		t := &m.chatThreads[m.chatThreadPos]
		t.Msgs = msgs
		t.Updated = time.Now()
		if m.chatModel != "" {
			t.Model = m.chatModel
		}
	} else {
		m.chatThreads = append([]chatThread{{
			Title:   chatThreadTitle(msgs),
			Model:   m.chatModel,
			Msgs:    msgs,
			Updated: time.Now(),
		}}, m.chatThreads...)
		m.chatThreadPos = 0
	}
	m.persistChatHistory()
}

// chatThreadTitle derives a thread title from the first user message.
func chatThreadTitle(msgs []ai.Message) string {
	for _, msg := range msgs {
		if msg.Role != "user" {
			continue
		}
		t := strings.Join(strings.Fields(msg.Content), " ")
		if r := []rune(t); len(r) > 48 {
			t = string(r[:48]) + "…"
		}
		return t
	}
	return "(no prompt)"
}

// resetChatConversation clears the in-memory conversation state without
// touching the prompt history.
func (m *Model) resetChatConversation() {
	m.cancelChat()
	m.chatMsgs = nil
	m.chatReply = ""
	m.chatErr = ""
	m.chatBusy = false
	m.chatToolRound = 0
	m.chatIn = nil
	m.chatScroll = 0
	m.chatReviewMode = false
	m.chatRunConfirm = ""
	m.rebuildChatRows()
}

// switchChatThread moves through the thread history: dir=+1 goes to older
// threads, dir=-1 to newer; from the newest one it opens a brand-new thread.
// Switching is blocked mid-stream or during diff review to avoid yanking
// state the tool loop is still working with.
func (m *Model) switchChatThread(dir int) {
	if m.chatBusy || m.chatReviewMode {
		return
	}
	m.saveChatThread()
	next := m.chatThreadPos + dir
	if next >= len(m.chatThreads) {
		return // already at the oldest thread
	}
	if next < 0 {
		m.chatThreadPos = -1
		m.resetChatConversation()
		m.msg = m.t("chat.thread_new")
		return
	}
	m.chatThreadPos = next
	m.resetChatConversation()
	th := m.chatThreads[next]
	m.chatMsgs = append([]ai.Message(nil), th.Msgs...)
	if th.Model != "" {
		m.chatModel = th.Model
	}
	m.rebuildChatRows()
	m.msg = m.t("chat.thread", th.Title)
}

// recordChatPrompt stores a sent prompt at the head of the input history and
// resets the history browsing position.
func (m *Model) recordChatPrompt(text string) {
	if len(m.chatPrompts) == 0 || m.chatPrompts[0] != text {
		m.chatPrompts = append([]string{text}, m.chatPrompts...)
	}
	m.chatPromptIdx = -1
	m.chatDraft = ""
	m.persistChatHistory()
}

// chatHistoryPrev recalls the previous sent prompt (Up in the input).
func (m *Model) chatHistoryPrev() {
	if len(m.chatPrompts) == 0 {
		return
	}
	if m.chatPromptIdx == -1 {
		m.chatDraft = string(m.chatIn)
		m.chatPromptIdx = 0
	} else if m.chatPromptIdx < len(m.chatPrompts)-1 {
		m.chatPromptIdx++
	}
	m.chatIn = []rune(m.chatPrompts[m.chatPromptIdx])
}


// clearChatHistory wipes all persisted threads and prompts (deletes the
// history file), resets the conversation and disarms any pending clear
// confirmation. Blocked mid-stream or during diff review.
func (m *Model) clearChatHistory() {
	if m.chatBusy || m.chatReviewMode {
		return
	}
	_ = os.Remove(chatHistoryPath(m.root))
	m.chatThreads = nil
	m.chatPrompts = nil
	m.chatThreadPos = -1
	m.chatPromptIdx = -1
	m.chatDraft = ""
	m.chatClearArm = false
	m.resetChatConversation()
	m.msg = m.t("chat.cleared")
}

// armChatClear implements the two-step Ctrl+L confirmation: the first press
// arms it, the second actually wipes the history. Any other chat key or
// leaving the panel disarms it.
func (m *Model) armChatClear() {
	if m.chatBusy || m.chatReviewMode {
		return
	}
	if m.chatClearArm {
		m.clearChatHistory()
		return
	}
	m.chatClearArm = true
	m.msg = m.t("chat.clear_confirm")
}

// chatHistoryNext moves back toward the newest prompt (Down in the input);
// past the newest one it restores the draft being typed.
func (m *Model) chatHistoryNext() {
	if m.chatPromptIdx == -1 {
		return
	}
	if m.chatPromptIdx > 0 {
		m.chatPromptIdx--
		m.chatIn = []rune(m.chatPrompts[m.chatPromptIdx])
		return
	}
	m.chatPromptIdx = -1
	if m.chatDraft != "" {
		m.chatIn = []rune(m.chatDraft)
		m.chatDraft = ""
	} else {
		m.chatIn = nil
	}
}
