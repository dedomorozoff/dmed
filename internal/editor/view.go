package editor

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"dmed/internal/syntax"
)

const tabWidth = 4

var (
	gutterStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	curGutterStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true)
	cursorStyle     = lipgloss.NewStyle().Reverse(true)
	statusStyle     = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("250"))
	statusHiStyle   = lipgloss.NewStyle().Background(lipgloss.Color("61")).Foreground(lipgloss.Color("255")).Bold(true)
	hintStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	activePaneStyle = lipgloss.NewStyle().Background(lipgloss.Color("235"))
	matchStyle      = lipgloss.NewStyle().Background(lipgloss.Color("214")).Foreground(lipgloss.Color("0"))
	curMatchStyle   = lipgloss.NewStyle().Background(lipgloss.Color("226")).Foreground(lipgloss.Color("0")).Bold(true)
)

type helpEntry struct {
	keys string
	desc string
}

var helpEntries = []helpEntry{
	{"Ctrl+S", "save active tab"},
	{"", ""},
	{"Ctrl+F", "search in file (Enter/F3 next, Shift+F3 prev)"},
	{"Ctrl+H", "search & replace (Tab switch, Enter rep, Ctrl+A all)"},
	{"Ctrl+O", "fuzzy file finder"},
	{"Ctrl+T", "open file by path"},
	{"Ctrl+B / F9", "project tree: show, focus, hide"},
	{"↑↓/Enter/←→ in tree", "navigate, open, fold"},
	{"Alt+←/→", "switch tabs in active pane"},
	{"Alt+1..9", "jump to tab N"},
	{"Ctrl+\\ / F6", "split vertical (side by side)"},
	{"Ctrl+Alt+H / F7", "split horizontal (stacked)"},
	{"Ctrl+Alt+P / F8", "focus other pane"},
	{"Ctrl+Alt+W", "close pane (unsplit)"},
	{"Ctrl+W / Ctrl+X", "close tab (last quits)"},
	{"", ""},
	{"Arrows/Home/End/PgUp/PgDn", "move cursor"},
	{"Enter/Backspace/Delete/Tab", "edit text"},
	{"Ctrl+Z / Ctrl+Y,Ctrl+R", "undo / redo"},
	{"", ""},
	{"F1 or Ctrl+E", "toggle this help"},
	{"Ctrl+Q / Ctrl+C", "quit"},
}

func (m Model) finderExtraRows() int {
	if !m.finderOpen {
		return 0
	}
	return len(m.finderHits) + 1
}

func (m Model) viewHeight() int {
	h := m.height - 2 - m.finderExtraRows()
	if h < 1 {
		h = 1
	}
	return h
}

func (m Model) gutterWidthForTab(t *tab) int {
	w := len(strconv.Itoa(t.buf.LineCount())) + 1
	if w < 4 {
		w = 4
	}
	return w
}

func (m Model) paneContentWidth(paneIdx int) int {
	t := &m.tabs[m.panes[paneIdx].tabIdx]
	return m.paneTotalWidth(paneIdx) - m.gutterWidthForTab(t)
}

func (m Model) View() string {
	h := m.viewHeight()
	rows := make([]string, 0, h+2)
	rows = append(rows, m.tabBar())
	if m.helpOpen {
		rows = append(rows, m.helpPanel(h)...)
	} else {
		rows = append(rows, m.editorRows(h)...)
	}
	bottom := m.statusBar()
	if m.promptOpen {
		bottom = m.promptLine()
	} else if m.searchOpen {
		if m.replaceOpen {
			bottom = m.replaceLine()
		} else {
			bottom = m.searchLine()
		}
	}
	rows = append(rows, bottom)
	if m.finderOpen {
		rows = append(rows, m.finderPanel()...)
	}
	return lipgloss.NewStyle().MaxWidth(m.width).Render(strings.Join(rows, "\n"))
}

func (m Model) editorRows(h int) []string {
	if m.layout == splitNone {
		return m.composeSidebar(m.renderPaneRows(0, h, m.paneTotalWidth(0)))
	}
	if m.layout == splitVert {
		w0 := m.paneTotalWidth(0)
		w1 := m.paneTotalWidth(1)
		left := m.renderPaneRows(0, h, w0)
		right := m.renderPaneRows(1, h, w1)
		combined := make([]string, h)
		sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
		sep := sepStyle.Render("│")
		for row := 0; row < h; row++ {
			combined[row] = left[row] + sep + right[row]
		}
		return m.composeSidebar(combined)
	}
	// splitHoriz
	h0 := m.paneViewHeight(0)
	h1 := m.paneViewHeight(1)
	top := m.renderPaneRows(0, h0, m.paneTotalWidth(0))
	bottom := m.renderPaneRows(1, h1, m.paneTotalWidth(1))
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	sep := sepStyle.Render(strings.Repeat("─", m.editorAreaWidth()))
	combined := make([]string, 0, h0+h1+1)
	combined = append(combined, top...)
	combined = append(combined, sep)
	combined = append(combined, bottom...)
	return m.composeSidebar(combined)
}

func (m Model) composeSidebar(editor []string) []string {
	if !m.sidebarOn() {
		return editor
	}
	tree := m.treePanel(len(editor))
	out := make([]string, len(editor))
	for row := range editor {
		out[row] = tree[row] + editor[row]
	}
	return out
}

func (m Model) renderPaneRows(paneIdx, h, totalW int) []string {
	p := &m.panes[paneIdx]
	t := &m.tabs[p.tabIdx]
	gw := m.gutterWidthForTab(t)
	contentW := totalW - gw
	if contentW < 0 {
		contentW = 0
	}
	cur := t.buf.CurLine()
	active := paneIdx == m.activePane
	rows := make([]string, h)

	syntaxLines := t.getSyntaxLines()

	for row := 0; row < h; row++ {
		ln := p.offsetY + row
		num := strconv.Itoa(ln + 1)
		gut := strings.Repeat(" ", gw-1-len(num)) + num + " "
		if active && ln == cur && ln < t.buf.LineCount() {
			rows[row] = curGutterStyle.Render(gut) + m.renderLine(p, t, ln, contentW, active, syntaxLines)
		} else {
			rows[row] = gutterStyle.Render(gut) + m.renderLine(p, t, ln, contentW, active, syntaxLines)
		}
		if active && m.layout != splitNone {
			rows[row] = activePaneStyle.Render(rows[row])
		}
	}
	return rows
}

func (m Model) sidebarWidth() int {
	if m.sidebarOn() {
		return treeWidth
	}
	return 0
}

func (m Model) tabBar() string {
	var parts []string
	activeTab := m.activeTabIndex()
	base := m.baseDir()
	for i := range m.tabs {
		t := &m.tabs[i]
		name := fmt.Sprintf(" %d:%s ", i+1, t.name(base))
		if t.buf.Dirty() {
			name += "* "
		}
		if i == activeTab {
			parts = append(parts, statusHiStyle.Render(name))
		} else {
			parts = append(parts, statusStyle.Render(name))
		}
	}
	line := strings.Join(parts, "")
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

func (m Model) treePanel(h int) []string {
	inner := treeWidth - 2
	rows := make([]string, 0, h)
	for row := 0; row < h; row++ {
		i := m.treeOffset + row
		var cell string
		if i < len(m.treeRows) {
			e := m.treeRows[i]
			indent := strings.Repeat("  ", e.depth-1)
			label := e.name
			if e.isDir {
				if m.expanded[e.rel] {
					label = "▾ " + label
				} else {
					label = "▸ " + label
				}
			} else {
				label = "  " + label
			}
			pad := inner - lipgloss.Width(indent) - lipgloss.Width(label)
			if pad < 0 {
				runes := []rune(label)
				label = string(runes[:maxInt(0, len(runes)+pad)])
				pad = 0
			}
			line := indent + label + strings.Repeat(" ", pad)
			if i == m.treeSel && m.treeFocus {
				cell = statusHiStyle.Render(line)
			} else if i == m.treeSel {
				cell = statusStyle.Render(line)
			} else {
				cell = line
			}
		} else {
			cell = strings.Repeat(" ", inner)
		}
		rows = append(rows, cell+" ")
	}
	return rows
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (m Model) helpPanel(h int) []string {
	rows := make([]string, 0, h)
	rows = append(rows, statusHiStyle.Render(" dmed — keys ")+" "+hintStyle.Render("(F1/Esc closes)"))
	for _, e := range helpEntries {
		if e.keys == "" {
			rows = append(rows, "")
			continue
		}
		key := e.keys
		if len(key) < 26 {
			key += strings.Repeat(" ", 26-len(key))
		}
		rows = append(rows, " "+statusStyle.Render(key)+e.desc)
	}
	return rows
}

func (m Model) promptLine() string {
	line := statusHiStyle.Render(" open file: ") + statusStyle.Render(string(m.promptIn)) + cursorStyle.Render(" ")
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

func (m Model) searchLine() string {
	line := statusHiStyle.Render(" search: ") + statusStyle.Render(string(m.searchQuery)) + cursorStyle.Render(" ")
	if len(m.searchQuery) > 0 {
		if m.searchTotalMatches > 0 {
			line += hintStyle.Render(fmt.Sprintf(" [%d/%d]", m.searchMatchIdx+1, m.searchTotalMatches))
		} else {
			line += hintStyle.Render(" [no matches]")
		}
	}
	hint := "  (Enter/F3: next, Shift+F3: prev, Esc: close)"
	line += hintStyle.Render(hint)
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

func (m Model) replaceLine() string {
	findPart := statusHiStyle.Render(" find: ") + statusStyle.Render(string(m.searchQuery))
	if m.replaceFocusFind {
		findPart += cursorStyle.Render(" ")
	} else {
		findPart += " "
	}

	repPart := statusHiStyle.Render(" replace: ") + statusStyle.Render(string(m.replaceWith))
	if !m.replaceFocusFind {
		repPart += cursorStyle.Render(" ")
	} else {
		repPart += " "
	}

	line := findPart + repPart
	if len(m.searchQuery) > 0 {
		if m.searchTotalMatches > 0 {
			line += hintStyle.Render(fmt.Sprintf(" [%d/%d]", m.searchMatchIdx+1, m.searchTotalMatches))
		} else {
			line += hintStyle.Render(" [no matches]")
		}
	}
	hint := "  (Tab: switch, Enter: replace, Ctrl+A: all, Esc: close)"
	line += hintStyle.Render(hint)
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

func (m Model) finderPanel() []string {
	rows := make([]string, 0, len(m.finderHits)+1)
	for i, hit := range m.finderHits {
		label := " " + hit + " "
		if i == m.finderSel {
			rows = append(rows, statusHiStyle.Render(label))
		} else {
			rows = append(rows, statusStyle.Render(label))
		}
	}
	line := statusHiStyle.Render(" find file: ") + statusStyle.Render(string(m.finderQ)) + cursorStyle.Render(" ")
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	rows = append(rows, line)
	return rows
}

func (m Model) renderLine(p *pane, t *tab, ln, w int, activePane bool, syntaxLines []syntax.HighlightedLine) string {
	if w <= 0 || ln >= t.buf.LineCount() {
		return ""
	}
	raw := t.buf.LineAt(ln)
	var rawStyles syntax.HighlightedLine
	if ln < len(syntaxLines) {
		rawStyles = syntaxLines[ln]
	}

	// Expand tabs
	exp := make([]rune, 0, len(raw))
	expStyles := make([]lipgloss.Style, 0, len(raw))
	rawToExp := make([]int, len(raw)+1)

	for i, r := range raw {
		rawToExp[i] = len(exp)
		var st lipgloss.Style
		if i < len(rawStyles) {
			st = rawStyles[i]
		}
		if r == '\t' {
			for k := 0; k < tabWidth; k++ {
				exp = append(exp, ' ')
				expStyles = append(expStyles, st)
			}
		} else {
			exp = append(exp, r)
			expStyles = append(expStyles, st)
		}
	}
	rawToExp[len(raw)] = len(exp)

	// Search match highlighting
	type matchInfo struct {
		start int
		end   int
		isCur bool
	}
	var matches []matchInfo
	if len(m.searchQuery) > 0 {
		qLen := len(m.searchQuery)
		matchCols := findMatchesInRunes(raw, m.searchQuery)
		for _, col := range matchCols {
			expStart := rawToExp[col]
			expEnd := rawToExp[col+qLen]
			isCur := (ln == t.buf.CurLine() && col == t.buf.Col())
			matches = append(matches, matchInfo{start: expStart, end: expEnd, isCur: isCur})
		}
	}

	// Cursor position in exp coordinates
	cx := -1
	if activePane && ln == t.buf.CurLine() {
		if t.buf.Col() <= len(raw) {
			cx = rawToExp[t.buf.Col()]
		}
	}

	// Visible window
	start := p.offsetX
	if start > len(exp) {
		start = len(exp)
	}
	end := start + w
	if end > len(exp) {
		end = len(exp)
	}

	var out strings.Builder
	for i := start; i < end; i++ {
		r := exp[i]
		st := expStyles[i]

		for _, mi := range matches {
			if i >= mi.start && i < mi.end {
				if mi.isCur {
					st = curMatchStyle
				} else {
					st = matchStyle
				}
				break
			}
		}

		if i == cx {
			st = cursorStyle
		}

		out.WriteString(st.Render(string(r)))
	}

	// Cursor at end of line
	if cx == len(exp) && cx >= start && cx < start+w {
		out.WriteString(cursorStyle.Render(" "))
	}

	return out.String()
}

func (m Model) statusBar() string {
	t := m.activeTab()
	base := m.baseDir()
	dirty := ""
	if t.buf.Dirty() {
		dirty = " *"
	}
	paneMark := ""
	if m.layout != splitNone {
		paneMark = fmt.Sprintf("[%d] ", m.activePane+1)
	}
	left := statusHiStyle.Render(" " + paneMark + t.name(base) + dirty)
	mid := ""
	if m.msg != "" {
		mid = statusStyle.Render("  " + m.msg)
	}
	right := fmt.Sprintf("Ln %d, Col %d ", t.buf.CurLine()+1, t.buf.Col()+1)
	hint := ""
	if !m.promptOpen && !m.finderOpen && !m.searchOpen {
		hint = "F1 help "
		if m.layout != splitNone {
			hint += "F8 pane "
		}
	}
	rightBar := hintStyle.Render(hint) + statusStyle.Render(right)
	fill := m.width - lipgloss.Width(left) - lipgloss.Width(mid) - lipgloss.Width(rightBar)
	if fill > 0 {
		return left + mid + statusStyle.Render(strings.Repeat(" ", fill)) + rightBar
	}
	return left + mid + rightBar
}

func expandTabs(l []rune) []rune {
	out := make([]rune, 0, len(l))
	for _, r := range l {
		if r == '\t' {
			for i := 0; i < tabWidth; i++ {
				out = append(out, ' ')
			}
		} else {
			out = append(out, r)
		}
	}
	return out
}

func visCol(l []rune, col int) int {
	x := 0
	for _, r := range l[:col] {
		if r == '\t' {
			x += tabWidth
		} else {
			x++
		}
	}
	return x
}
