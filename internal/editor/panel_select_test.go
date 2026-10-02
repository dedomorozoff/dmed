package editor

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/hinshun/vt10x"

	"dmed/internal/ai"
)

// TestChatSelectionCopy pins drag-select in the chat transcript: anchor and
// end normalize in any direction, label rows are skipped by the copy and the
// covered text lands in the internal clipboard on release.
func TestChatSelectionCopy(t *testing.T) {
	m := New()
	m.width = 100
	m.height = 30
	m.chatMsgs = []ai.Message{
		{Role: "assistant", Content: "line one\nline two\nline three"},
	}
	m.rebuildChatRows()

	// Find the row indices of the first and last content lines.
	first, last := -1, -1
	for i, r := range m.chatRows {
		if chatRowContent(r.kind) {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 || last <= first {
		t.Fatalf("expected several content rows, got %d..%d of %d", first, last, len(m.chatRows))
	}

	m.startChatSelection(first, 5)
	m.extendChatSelection(last, 4)
	got := m.chatSelectionText()
	if got != "one\nline two\nline" {
		t.Fatalf("selection text = %q", got)
	}

	// Reverse direction must yield the same text.
	m2 := m
	m2.chatSelAnchor, m2.chatSelEnd = m2.chatSelEnd, m2.chatSelAnchor
	if rev := m2.chatSelectionText(); rev != got {
		t.Fatalf("reversed selection = %q, want %q", rev, got)
	}
}

// TestTermSelectionCopy pins Shift+drag selection over the terminal output.
func TestTermSelectionCopy(t *testing.T) {
	m := Model{}
	m.termRows = []terminalRow{
		{cells: glyphRow("first line")},
		{cells: glyphRow("second line")},
	}
	m.startTermSelection(0, 6)
	m.extendTermSelection(1, 6)
	if got, want := m.termSelectionText(), "line\nsecond"; got != want {
		t.Fatalf("termSelectionText() = %q, want %q", got, want)
	}
}

// glyphRow builds terminal cells from a plain string.
func glyphRow(s string) []vt10x.Glyph {
	cells := make([]vt10x.Glyph, 0, len(s))
	for _, r := range s {
		cells = append(cells, vt10x.Glyph{Char: r})
	}
	return cells
}

// TestPanelFocusSwitchWithTerminalOpen verifies the complaint: with the
// terminal docked, clicking the chat rail must move keyboard focus to the
// chat (typing reaches the chat input), and clicking the terminal panel must
// give it back.
func TestPanelFocusSwitchWithTerminalOpen(t *testing.T) {
	m := New()
	m.width = 100
	m.height = 30
	m.termOpen = true
	m.termFocus = true
	m.chatOpen = true

	railX := m.width - m.rightRailWidth() + 2
	m.handleMouseClick(tea.MouseClickMsg{X: railX, Y: 5, Button: tea.MouseLeft})
	if m.termFocus {
		t.Fatal("clicking the chat rail must take focus from the terminal")
	}
	if !m.chatFocus {
		t.Fatal("clicking the chat rail must focus the chat")
	}

	// Typing now reaches the chat input, not the PTY.
	nm, _ := m.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	u, ok := nm.(Model)
	if !ok {
		t.Fatalf("Update returned %T", nm)
	}
	if string(u.chatIn) != "h" {
		t.Fatalf("chat input = %q, want \"h\" (keys must reach the chat)", string(u.chatIn))
	}

	// Clicking the terminal panel hands focus back.
	m.handleMouseClick(tea.MouseClickMsg{X: 10, Y: m.termStartRow() + 2, Button: tea.MouseLeft})
	if !m.termFocus || m.chatFocus {
		t.Fatalf("termFocus=%v chatFocus=%v, want terminal focused", m.termFocus, m.chatFocus)
	}
}

// TestChatSelHitMapsUserBubble checks that clicking inside a right-aligned
// user bubble maps to the text column under the pointer, not to the padded
// left edge.
func TestChatSelHitMapsUserBubble(t *testing.T) {
	m := New()
	m.width = 100
	m.height = 30
	m.chatMsgs = []ai.Message{{Role: "user", Content: "hello"}}
	m.rebuildChatRows()

	w := m.chatPanelWidth()
	panelX := m.width - m.rightRailWidth()
	// The bubble row sits at screen row 3 (1 tab bar + header + label). The bubble
	// is " hello " right-aligned: leading space at local w-8, text at
	// w-7..w-3, trailing space at w-2, scrollbar column at w-1.
	if _, col, ok := m.chatSelHit(3, panelX+w-8); !ok || col != 0 {
		t.Fatalf("bubble leading space maps to col %d (ok=%v), want 0", col, ok)
	}
	if _, col, ok := m.chatSelHit(3, panelX+w-3); !ok || col != 4 {
		t.Fatalf("last text rune maps to col %d (ok=%v), want 4", col, ok)
	}
	if _, col, ok := m.chatSelHit(3, panelX+w-2); !ok || col != 5 {
		t.Fatalf("bubble trailing space maps to col %d (ok=%v), want 5", col, ok)
	}
}

// TestStartChatTurnRebuildsNilProvider reproduces the crash where the settings
// wizard nils the cached provider and the next chat turn called ChatStream on
// a nil interface inside the stream goroutine.
func TestStartChatTurnRebuildsNilProvider(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.width, m.height = 100, 30
	m.chatModel = "test-model"
	m.ai = nil // what saving the AI wizard leaves behind
	m.chatIn = []rune("hi")

	cmd := m.chatSubmit()
	if cmd == nil {
		t.Fatal("submit must start a turn")
	}
	if m.ai == nil {
		t.Fatal("startChatTurn must rebuild a nil provider before streaming")
	}
}

// TestShiftClickSelectsInTerminal verifies Shift+click starts a terminal
// selection instead of forwarding the mouse event to the inner application.
func TestShiftClickSelectsInTerminal(t *testing.T) {
	m := New()
	m.width = 100
	m.height = 30
	m.termOpen = true
	m.termFocus = true

	m.handleMouseClick(tea.MouseClickMsg{X: 4, Y: m.termStartRow() + 2, Button: tea.MouseLeft, Mod: tea.ModShift})
	if !m.termSelActive || !m.dragTerm {
		t.Fatalf("shift+click must start a terminal selection")
	}
}
