package editor

import (
	"strings"

	"github.com/atotto/clipboard"
)

// Text selection for the two read-only panels: the AI chat transcript and the
// terminal output. Both store their content as fixed rows, so a selection is
// a (row, visible column) span; on mouse release the covered text goes to the
// system clipboard, the same behavior as selecting in a browser.

type selPos struct {
	row int
	col int
}

func lessSel(a, b selPos) bool {
	return a.row < b.row || (a.row == b.row && a.col < b.col)
}

// copyToClipboard stores text in the internal and system clipboards.
func (m *Model) copyToClipboard(text string) {
	if text == "" {
		return
	}
	m.clipboard = text
	if err := clipboard.WriteAll(text); err != nil {
		m.msg = "clipboard: " + err.Error()
		return
	}
	m.msg = m.t("msg.copied")
}

// ---- Chat transcript selection ---------------------------------------------

// chatRowPlain returns the visible text of a row (segments flattened).
func chatRowPlain(r chatRow) string {
	if len(r.rich) == 0 {
		return r.text
	}
	var b strings.Builder
	for _, s := range r.rich {
		b.WriteString(s.text)
	}
	return b.String()
}

// chatRowContent reports whether a row carries message content worth
// copying; label and filler rows are skipped by the copy.
func chatRowContent(kind string) bool {
	switch kind {
	case "user", "ai", "ai-code", "ai-head", "ai-quote", "tool", "todo", "todo-done", "notice", "err":
		return true
	}
	return false
}

func (m *Model) startChatSelection(row, col int) {
	m.chatSelAnchor = selPos{row, col}
	m.chatSelEnd = selPos{row, col}
	m.chatSelActive = true
}

// chatSelHit maps a screen position inside the chat transcript to a
// (chatRows index, column in that row's plain text); ok=false when the point
// is outside the body rows (header, buttons, input) or past the transcript.
// The column is kind-aware: user rows are right-aligned bubbles, so their
// text starts near the right edge, every other row starts at the body pad.
func (m Model) chatSelHit(y, x int) (int, int, bool) {
	if m.chatReviewMode {
		return 0, 0, false
	}
	start, bodyH := m.chatBodyRange(len(m.chatRows))
	rel := y - 2 // the body starts at screen row 2, under header row 1
	if rel < 0 || rel >= bodyH {
		return 0, 0, false
	}
	idx := start + rel
	if idx < 0 || idx >= len(m.chatRows) {
		return 0, 0, false
	}
	r := m.chatRows[idx]
	plainW := len([]rune(chatRowPlain(r)))
	local := x - (m.width - m.rightRailWidth())
	col := local - chatBodyPad
	if r.kind == "user" {
		// bubble = " " + text + " " right-aligned one column clear of the
		// scrollbar, so the text begins at w - plainW - 2.
		col = local - (m.chatPanelWidth() - plainW - 2)
	}
	if col < 0 {
		col = 0
	}
	if col > plainW {
		col = plainW
	}
	return idx, col, true
}

func (m *Model) extendChatSelection(row, col int) {
	m.chatSelEnd = selPos{row, col}
}

// clearChatSelection drops the highlight (next click starts a new one).
func (m *Model) clearChatSelection() {
	m.chatSelActive = false
}

// chatSelRange returns the selected column range on a row; ok=false when the
// row is not part of the selection.
func (m Model) chatSelRange(row, width int) (int, int, bool) {
	if !m.chatSelActive || width < 0 {
		return 0, 0, false
	}
	a, b := m.chatSelAnchor, m.chatSelEnd
	if lessSel(b, a) {
		a, b = b, a
	}
	if row < a.row || row > b.row {
		return 0, 0, false
	}
	c0 := 0
	if row == a.row {
		c0 = a.col
	}
	c1 := width
	if row == b.row {
		c1 = b.col
	}
	if c0 < 0 {
		c0 = 0
	}
	if c1 > width {
		c1 = width
	}
	if c1 <= c0 {
		return 0, 0, false
	}
	return c0, c1, true
}

// chatSelectionText collects the text covered by the selection: content rows
// contribute their selected slice, label and filler rows are skipped.
func (m Model) chatSelectionText() string {
	if !m.chatSelActive {
		return ""
	}
	a, b := m.chatSelAnchor, m.chatSelEnd
	if lessSel(b, a) {
		a, b = b, a
	}
	var parts []string
	for i := a.row; i <= b.row && i < len(m.chatRows); i++ {
		if i < 0 || !chatRowContent(m.chatRows[i].kind) {
			continue
		}
		runes := []rune(chatRowPlain(m.chatRows[i]))
		c0, c1, ok := m.chatSelRange(i, len(runes))
		if !ok {
			continue
		}
		parts = append(parts, string(runes[c0:c1]))
	}
	return strings.Join(parts, "\n")
}

// ---- Terminal output selection ---------------------------------------------

func (m *Model) startTermSelection(row, col int) {
	m.termSelAnchor = selPos{row, col}
	m.termSelEnd = selPos{row, col}
	m.termSelActive = true
}

func (m *Model) extendTermSelection(row, col int) {
	m.termSelEnd = selPos{row, col}
}

// termSelRange returns the selected column range on a terminal row.
func (m Model) termSelRange(row, width int) (int, int, bool) {
	if !m.termSelActive || width < 0 {
		return 0, 0, false
	}
	a, b := m.termSelAnchor, m.termSelEnd
	if lessSel(b, a) {
		a, b = b, a
	}
	if row < a.row || row > b.row {
		return 0, 0, false
	}
	c0 := 0
	if row == a.row {
		c0 = a.col
	}
	c1 := width
	if row == b.row {
		c1 = b.col
	}
	if c0 < 0 {
		c0 = 0
	}
	if c1 > width {
		c1 = width
	}
	if c1 <= c0 {
		return 0, 0, false
	}
	return c0, c1, true
}

// termSelectionText collects the selected terminal cells, trimming the
// trailing spaces of every covered line.
func (m Model) termSelectionText() string {
	if !m.termSelActive {
		return ""
	}
	a, b := m.termSelAnchor, m.termSelEnd
	if lessSel(b, a) {
		a, b = b, a
	}
	var lines []string
	for i := a.row; i <= b.row && i < len(m.termRows); i++ {
		if i < 0 {
			continue
		}
		runes := []rune(terminalRowText(m.termRows[i].cells))
		c0, c1, ok := m.termSelRange(i, len(runes))
		if !ok {
			continue
		}
		lines = append(lines, string(runes[c0:c1]))
	}
	return strings.Join(lines, "\n")
}
