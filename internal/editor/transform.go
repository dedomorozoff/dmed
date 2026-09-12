package editor

import (
	"strings"

	"dmed/internal/buffer"
)

// uppercaseActive uppercases the current selection, or the whole buffer when
// there is no selection.
func (m *Model) uppercaseActive() {
	b := m.cur().buf
	if b.HasSelection() {
		text := b.SelectedText()
		b.DeleteSelection()
		b.InsertText(strings.ToUpper(text))
	} else {
		m.cur().buf = buffer.Load(strings.ToUpper(m.cur().buf.Text()))
	}
	m.msg = m.t("msg.uppercased")
}
