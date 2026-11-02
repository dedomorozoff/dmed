package editor

import (
	"strings"

	"github.com/atotto/clipboard"
)

// The column (rectangular) selection commands. Like the jump list and the bracket
// commands these are selection moves: no overlay, no new mode, nothing written to
// disk. The rectangle is the buffer's business — its selection is two corners, and
// a column block is the same corners read as a rectangle (internal/buffer/
// column.go). What is left here is the chord, the status line, and the editor's
// memory of whether the text in the clipboard was copied as a block.

// growColumnSelection moves the rectangle's caret corner — the four
// Ctrl+Alt+Shift+arrow chords. The first press enters column mode from the caret
// or from a linear selection's bounding box; a corner that cannot move (past the
// first or the last row, or left from column 0) leaves the selection as it was.
func (m *Model) growColumnSelection(dRow, dCol int) {
	m.cur().buf.GrowColumn(dRow, dCol)
	m.msg = ""
}

// toggleColumnSelectionActive reinterprets the current selection as a rectangle,
// or back as the span between its corners. It is the palette's route: the chords
// grow a block from nothing, while this one needs a selection to read differently.
func (m *Model) toggleColumnSelectionActive() {
	b := m.cur().buf
	if !b.HasSelection() {
		m.msg = m.t("msg.col_select_none")
		return
	}
	b.SetColumnSelection(!b.IsColumnSelection())
	if b.IsColumnSelection() {
		m.msg = m.t("msg.col_select_on")
	} else {
		m.msg = m.t("msg.col_select_off")
	}
}

// copySelection puts the selection in both clipboards and records whether it came
// off a rectangle. The system clipboard carries no type, so that flag is the only
// way a later paste can tell a block back — see pasteClipboard.
func (m *Model) copySelection() {
	b := m.cur().buf
	m.clipboard = b.SelectedText()
	m.clipboardBlock = b.IsColumnSelection()
	clipboard.WriteAll(m.clipboard)
	m.msg = m.t("msg.copied")
}

// cutSelection copies and then removes the selection. A rectangle removes its cell
// on every row, which is the buffer's decision, not this one.
func (m *Model) cutSelection() {
	m.copySelection()
	m.cur().buf.DeleteSelection()
	m.msg = m.t("msg.cut")
}

// pasteClipboard inserts the clipboard at the caret. A block that was copied here
// and is still unchanged in the clipboard unfolds: one line of it per row, the way
// it was picked up. Text from anywhere else is ordinary text — no clipboard
// records that it used to be a rectangle.
func (m *Model) pasteClipboard(text string) {
	if text == "" {
		return
	}
	b := m.cur().buf
	if m.clipboardBlock && text == m.clipboard {
		b.InsertColumn(strings.Split(text, "\n"))
		m.msg = m.t("msg.pasted")
		return
	}
	if b.HasMultipleCursors() {
		b.MultiInsertText(text)
	} else {
		b.InsertText(text)
	}
	m.msg = m.t("msg.pasted")
}
