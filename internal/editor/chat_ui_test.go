package editor

import (
	"strings"
	"testing"

	"dmed/internal/ai"
)

// TestChatButtonStripHitAndActivate pins the pseudo-button row: buttons are
// hit-tested by screen column, fire their action on click, and unknown cells
// resolve to nothing.
func TestChatButtonStripHitAndActivate(t *testing.T) {
	m := New()
	m.width = 100
	m.height = 30
	m.toggleChat()
	m.chatMsgs = []ai.Message{{Role: "user", Content: "hi"}}

	if row := m.chatButtonsRow(); row < 0 {
		t.Fatalf("button strip row = %d, want a visible row", row)
	}
	// Panel width is 40, so the right-aligned strip of four 3-wide cells
	// starts at panel column 27, i.e. screen column 60+27=87.
	if got := m.chatButtonAt(86); got != chatBtnNone {
		t.Errorf("column left of the first button = %v, want none", got)
	}
	if got := m.chatButtonAt(87); got != chatBtnNew {
		t.Errorf("first button cell = %v, want chatBtnNew", got)
	}

	m.activateChatButton(chatBtnNew)
	if len(m.chatMsgs) != 0 {
		t.Fatalf("new-thread button must reset the conversation, msgs = %d", len(m.chatMsgs))
	}

	m.toggleChat()
	m.activateChatButton(chatBtnClose)
	if m.chatOpen {
		t.Fatal("close button must close the chat panel")
	}
}

// TestChatButtonTooltip shows the label and key combination of the hovered
// button in a floating callout above the strip.
func TestChatButtonTooltip(t *testing.T) {
	m := New()
	m.width = 100
	m.height = 30
	m.chatOpen = true
	m.hoverChatBtn = chatBtnCopy

	rows := make([]string, m.viewHeight()+2)
	for i := range rows {
		rows[i] = "filler"
	}
	out := m.overlayChatTooltip(rows)
	if !strings.Contains(out[m.chatButtonsRow()-1], m.t("chat.tip_copy")) {
		t.Fatalf("hover callout must show the tip %q, row = %q", m.t("chat.tip_copy"), out[m.chatButtonsRow()-1])
	}

	m.hoverChatBtn = chatBtnNone
	for i := range rows {
		rows[i] = "filler"
	}
	out = m.overlayChatTooltip(rows)
	if out[m.chatButtonsRow()-1] != "filler" {
		t.Fatalf("no hover, no callout: %q", out[m.chatButtonsRow()-1])
	}
}

// TestChatScrollbarAppearsWhenNeeded verifies the one-column scrollbar shows
// a thumb only when the transcript overflows the body, and disappears when
// everything fits.
func TestChatScrollbarAppearsWhenNeeded(t *testing.T) {
	m := New()
	m.width = 100
	m.height = 30

	m.chatReply = strings.Repeat("word ", 200) // wraps into many body rows
	m.rebuildChatRows()
	panel := m.chatPanel(20)
	if !strings.Contains(strings.Join(panel, "\n"), m.g.progFull) {
		t.Fatalf("overflowing transcript must show the scrollbar thumb")
	}
	if !strings.Contains(strings.Join(panel, "\n"), m.g.vline) {
		t.Fatalf("scrollbar track missing")
	}

	m.chatReply = "short"
	m.rebuildChatRows()
	panel = m.chatPanel(20)
	if strings.Contains(strings.Join(panel, "\n"), m.g.progFull) {
		t.Fatalf("scrollbar must disappear when the transcript fits")
	}
}

// TestChatUserBubbleRightAligned checks the chat look: user text rows are
// right-aligned in a bubble instead of hugging the left edge.
func TestChatUserBubbleRightAligned(t *testing.T) {
	m := New()
	m.width = 100
	m.height = 30
	m.chatMsgs = []ai.Message{{Role: "user", Content: "hello there"}}
	m.rebuildChatRows()

	panel := m.chatPanel(20)
	found := false
	for _, row := range panel {
		if !strings.Contains(row, "hello there") {
			continue
		}
		found = true
		lead := len(row) - len(strings.TrimLeft(row, " "))
		if lead <= chatBodyPad {
			t.Fatalf("user bubble must be right-aligned, leading spaces = %d, row = %q", lead, row)
		}
	}
	if !found {
		t.Fatal("user message row not found in the panel")
	}
}
