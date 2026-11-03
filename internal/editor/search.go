package editor

import (
	tea "charm.land/bubbletea/v2"
)

func (m *Model) startSearch() {
	m.searchOpen = true
	m.replaceOpen = false
	m.replaceFocusFind = true
	if len(m.searchQuery) > 0 {
		m.updateSearchMatches(false)
	}
}

func (m *Model) startReplace() {
	m.searchOpen = true
	m.replaceOpen = true
	m.replaceFocusFind = false
	if len(m.searchQuery) > 0 {
		m.updateSearchMatches(false)
	}
}

func (m *Model) handleSearch(s string, msg tea.KeyPressMsg) tea.Cmd {
	switch m.paneCmd("find", s) {
	case "find.close":
		m.searchOpen = false
		m.replaceOpen = false
		m.msg = ""
	case "find.next":
		if len(m.searchQuery) > 0 {
			m.updateSearchMatches(true)
		}
	case "find.prev":
		if len(m.searchQuery) > 0 {
			m.findPrev()
		}
	case "find.replace":
		m.startReplace()
	case "find.regex":
		// Regex mode toggle. Applies to this prompt and to Ctrl+H replace
		// (same query); the hint row shows the (.* state.
		m.searchRegex = !m.searchRegex
		m.updateSearchMatches(false)
	case "find.erase":
		if n := len(m.searchQuery); n > 0 {
			m.searchQuery = m.searchQuery[:n-1]
			m.updateSearchMatches(false)
		}
	case paneUnbound:
		return nil
	default:
		if len(msg.Text) > 0 {
			m.searchQuery = append(m.searchQuery, []rune(msg.Text)...)
			m.updateSearchMatches(false)
		}
	}
	return nil
}

func (m *Model) handleReplace(s string, msg tea.KeyPressMsg) tea.Cmd {
	switch m.paneCmd("replace", s) {
	case "replace.close":
		m.searchOpen = false
		m.replaceOpen = false
		m.msg = ""
	case "replace.focus":
		m.replaceFocusFind = !m.replaceFocusFind
	case "replace.all":
		m.doReplaceAll()
	case "replace.accept":
		if m.replaceFocusFind {
			m.updateSearchMatches(true)
		} else {
			m.doReplace()
		}
	case "replace.next":
		m.updateSearchMatches(true)
	case "replace.prev":
		m.findPrev()
	case "replace.erase":
		if m.replaceFocusFind {
			if n := len(m.searchQuery); n > 0 {
				m.searchQuery = m.searchQuery[:n-1]
				m.updateSearchMatches(false)
			}
		} else {
			if n := len(m.replaceWith); n > 0 {
				m.replaceWith = m.replaceWith[:n-1]
			}
		}
	case paneUnbound:
		return nil
	default:
		if len(msg.Text) > 0 {
			if m.replaceFocusFind {
				m.searchQuery = append(m.searchQuery, []rune(msg.Text)...)
				m.updateSearchMatches(false)
			} else {
				m.replaceWith = append(m.replaceWith, []rune(msg.Text)...)
			}
		}
	}
	return nil
}

type searchMatch struct {
	line   int // 0-based buffer line; 0 for per-line matches
	col    int
	length int
}

func (m *Model) allMatches() []searchMatch {
	if len(m.searchQuery) == 0 {
		return nil
	}
	t := m.cur()
	var matches []searchMatch
	for ln := 0; ln < t.buf.LineCount(); ln++ {
		for _, hit := range m.matchRanges(t.buf.LineAt(ln)) {
			matches = append(matches, searchMatch{line: ln, col: hit.col, length: hit.length})
		}
	}
	return matches
}

func (m *Model) updateSearchMatches(jumpToNext bool) {
	matches := m.allMatches()
	m.searchTotalMatches = len(matches)
	if len(matches) == 0 {
		m.searchMatchIdx = -1
		return
	}
	t := m.cur()
	curLine := t.buf.CurLine()
	curCol := t.buf.Col()

	foundIdx := 0
	for i, mPos := range matches {
		if mPos.line > curLine || (mPos.line == curLine && mPos.col >= curCol) {
			foundIdx = i
			break
		}
	}
	if jumpToNext && m.searchMatchIdx >= 0 {
		foundIdx = (m.searchMatchIdx + 1) % len(matches)
	}
	m.searchMatchIdx = foundIdx
	target := matches[foundIdx]
	t.buf.SetCursor(target.line, target.col)
}

func (m *Model) findPrev() {
	matches := m.allMatches()
	m.searchTotalMatches = len(matches)
	if len(matches) == 0 {
		m.searchMatchIdx = -1
		return
	}
	if m.searchMatchIdx <= 0 {
		m.searchMatchIdx = len(matches) - 1
	} else {
		m.searchMatchIdx--
	}
	target := matches[m.searchMatchIdx]
	m.cur().buf.SetCursor(target.line, target.col)
}

func (m *Model) doReplace() {
	if len(m.searchQuery) == 0 {
		return
	}
	t := m.cur()
	matches := m.allMatches()
	if len(matches) == 0 {
		return
	}
	curLine := t.buf.CurLine()
	curCol := t.buf.Col()

	onMatch := 0
	for _, mPos := range matches {
		if mPos.line == curLine && mPos.col == curCol {
			onMatch = mPos.length
			break
		}
	}
	if onMatch == 0 {
		m.updateSearchMatches(false)
		return
	}

	// Regex mode replaces the whole matched range; literal mode replaces
	// exactly the query length (the two only differ when the pattern has run
	// wild, and then they are the same too).
	t.buf.ReplaceRange(curLine, curCol, onMatch, m.replaceWith)
	m.msg = m.t("msg.replaced_one")
	m.updateSearchMatches(false)
}

func (m *Model) doReplaceAll() {
	if len(m.searchQuery) == 0 {
		return
	}
	t := m.cur()
	var count int
	if m.searchRegex {
		re := m.searchRegexCompiled()
		if re == nil {
			// Invalid pattern: never fall back to literal here — the user
			// asked to replace by pattern, not by the pattern's raw text.
			return
		}
		count = t.buf.ReplaceAllRE(re, string(m.replaceWith))
	} else {
		count = t.buf.ReplaceAll(string(m.searchQuery), string(m.replaceWith))
	}
	if count == 1 {
		m.msg = m.t("msg.replaced_one")
	} else {
		m.msg = m.t("msg.replaced_many", count)
	}
	m.searchOpen = false
	m.replaceOpen = false
}
