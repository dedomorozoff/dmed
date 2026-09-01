package editor

import (
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const complMaxCandidates = 2000
const complVisible = 8

func isWordRune(r rune) bool {
	if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
		return true
	}
	return r == '_'
}

// complPrefix returns the start column and text of the word being completed at
// the cursor on the current line.
func (m *Model) complPrefix() (int, string) {
	b := m.cur().buf
	line := b.LineAt(b.CurLine())
	col := b.Col()
	start := col
	for start > 0 && isWordRune(line[start-1]) {
		start--
	}
	return start, string(line[start:col])
}

// complWordCandidates collects identifiers from the current buffer that start
// with prefix (excluding an exact match), deduplicated and sorted.
func (m *Model) complWordCandidates(prefix string) []string {
	t := m.cur()
	if t == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	n := t.buf.LineCount()
	for i := 0; i < n && len(out) < complMaxCandidates; i++ {
		line := t.buf.LineAt(i)
		j := 0
		for j < len(line) {
			for j < len(line) && !isWordRune(line[j]) {
				j++
			}
			start := j
			for j < len(line) && isWordRune(line[j]) {
				j++
			}
			if start < j {
				w := string(line[start:j])
				if w != prefix && strings.HasPrefix(w, prefix) && !seen[w] {
					seen[w] = true
					out = append(out, w)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// triggerCompletion recomputes candidates for the word at the cursor and opens
// or closes the popup. force ignores whether candidates were found, so
// Ctrl+Space can surface suggestions even mid-word. It returns a command that
// fires an async LSP completion request when a language server is available.
func (m *Model) triggerCompletion(force bool) tea.Cmd {
	start, prefix := m.complPrefix()
	if prefix == "" && !force {
		m.closeCompletion()
		return nil
	}
	cands := m.complWordCandidates(prefix)
	if len(cands) == 0 && !force {
		m.closeCompletion()
		return nil
	}
	m.complOpen = true
	m.complItems = cands
	m.complStart = start
	m.complLine = m.cur().buf.CurLine()
	m.complSel = 0
	m.complOffset = 0
	return m.lspCompletionCmd()
}

func (m *Model) closeCompletion() {
	m.complOpen = false
	m.complItems = nil
	m.complSel = 0
	m.complOffset = 0
}

// acceptCompletion replaces the completed word with the selected item.
func (m *Model) acceptCompletion() {
	if !m.complOpen || len(m.complItems) == 0 {
		return
	}
	b := m.cur().buf
	if b.CurLine() != m.complLine {
		m.closeCompletion()
		return
	}
	item := m.complItems[m.complSel]
	to := b.Col()
	from := m.complStart
	if to < from {
		from = to
	}
	b.ReplaceRange(b.CurLine(), from, to-from, []rune(item))
	m.closeCompletion()
}

// handleCompletionKey processes navigation keys while the popup is open. It
// returns true when the key was consumed.
func (m *Model) handleCompletionKey(key string) bool {
	n := len(m.complItems)
	switch key {
	case "esc":
		m.closeCompletion()
		return true
	case "tab", "enter":
		m.acceptCompletion()
		return true
	case "up", "ctrl+p":
		if n > 0 {
			m.complSel = (m.complSel - 1 + n) % n
			m.clampCompletion()
		}
		return true
	case "down", "ctrl+n":
		if n > 0 {
			m.complSel = (m.complSel + 1) % n
			m.clampCompletion()
		}
		return true
	case "pgup":
		if n > 0 {
			m.complSel -= complVisible
			if m.complSel < 0 {
				m.complSel = 0
			}
			m.clampCompletion()
		}
		return true
	case "pgdn":
		if n > 0 {
			m.complSel += complVisible
			if m.complSel > n-1 {
				m.complSel = n - 1
			}
			m.clampCompletion()
		}
		return true
	case "left", "right", "home", "end":
		m.closeCompletion()
		return false
	}
	return false
}

// clampCompletion keeps the selection within the visible popup window.
func (m *Model) clampCompletion() {
	if m.complSel < m.complOffset {
		m.complOffset = m.complSel
	}
	if m.complSel >= m.complOffset+complVisible {
		m.complOffset = m.complSel - complVisible + 1
	}
}

// complExtraRows reports how many rows the completion popup occupies.
func (m Model) complExtraRows() int {
	if !m.complOpen {
		return 0
	}
	n := len(m.complItems)
	if n > complVisible {
		n = complVisible
	}
	return n + 1
}

// complPanel renders the completion popup as a compact list.
func (m Model) complPanel() []string {
	items := m.complItems
	total := len(items)
	if total > complVisible {
		items = items[m.complOffset : m.complOffset+complVisible]
	}
	rows := make([]string, 0, len(items)+1)
	rows = append(rows, statusHiStyle.Render(" "+m.t("compl.title")+" "))
	for i, it := range items {
		label := " " + it + " "
		pad := m.width - lipgloss.Width(label)
		if pad < 0 {
			pad = 0
		}
		label += strings.Repeat(" ", pad)
		if m.complOffset+i == m.complSel {
			rows = append(rows, statusHiStyle.Render(label))
		} else {
			rows = append(rows, statusStyle.Render(label))
		}
	}
	return rows
}
