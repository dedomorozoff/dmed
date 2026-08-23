package editor

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const tabWidth = 4

var (
	gutterStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	curGutterStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true)
	cursorStyle    = lipgloss.NewStyle().Reverse(true)
	statusStyle    = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("250"))
	statusHiStyle  = lipgloss.NewStyle().Background(lipgloss.Color("61")).Foreground(lipgloss.Color("255")).Bold(true)
	hintStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
)

type helpEntry struct {
	keys string
	desc string
}

var helpEntries = []helpEntry{
	{"Ctrl+S", "save active tab"},
	{"", ""},
	{"Ctrl+O / F3", "fuzzy file finder"},
	{"Ctrl+T", "open file by path"},
	{"Ctrl+B / F9", "project tree: show, focus, hide"},
	{"↑↓/Enter/←→ in tree", "navigate, open, fold"},
	{"Alt+←/→", "switch tabs"},
	{"Alt+1..9", "jump to tab N"},
	{"Ctrl+W", "close tab (last quits)"},
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

func (m Model) gutterWidth() int {
	w := len(strconv.Itoa(m.activeTab().buf.LineCount())) + 1
	if w < 4 {
		w = 4
	}
	return w
}

func (m Model) viewWidth() int { return m.width - m.sidebarWidth() - m.gutterWidth() }

func (m Model) sidebarWidth() int {
	if m.sidebarOn() {
		return treeWidth
	}
	return 0
}

func (m Model) View() string {
	h := m.viewHeight()
	gw := m.gutterWidth()
	w := m.width - gw
	t := m.activeTab()
	cur := t.buf.CurLine()
	rows := make([]string, 0, h+2)
	rows = append(rows, m.tabBar())
	if m.helpOpen {
		rows = append(rows, m.helpPanel(h)...)
	} else if m.sidebarOn() {
		tree := m.treePanel(h)
		for row := 0; row < h; row++ {
			ln := t.offsetY + row
			num := strconv.Itoa(ln + 1)
			gut := strings.Repeat(" ", gw-1-len(num)) + num + " "
			cell := ""
			if ln == cur && ln < t.buf.LineCount() {
				cell = curGutterStyle.Render(gut)
			} else {
				cell = gutterStyle.Render(gut)
			}
			rows = append(rows, tree[row]+cell+m.renderContent(t, ln, w))
		}
	} else {
		for row := 0; row < h; row++ {
			ln := t.offsetY + row
			num := strconv.Itoa(ln + 1)
			gut := strings.Repeat(" ", gw-1-len(num)) + num + " "
			if ln == cur && ln < t.buf.LineCount() {
				rows = append(rows, curGutterStyle.Render(gut)+m.renderContent(t, ln, w))
			} else {
				rows = append(rows, gutterStyle.Render(gut)+m.renderContent(t, ln, w))
			}
		}
	}
	bottom := m.statusBar()
	if m.promptOpen {
		bottom = m.promptLine()
	}
	rows = append(rows, bottom)
	if m.finderOpen {
		rows = append(rows, m.finderPanel()...)
	}
	return lipgloss.NewStyle().MaxWidth(m.width).Render(strings.Join(rows, "\n"))
}

func (m Model) tabBar() string {
	base := m.baseDir()
	parts := make([]string, 0, len(m.tabs))
	for i := range m.tabs {
		t := &m.tabs[i]
		label := " " + t.name(base)
		if t.buf.Dirty() {
			label += "*"
		}
		label += " "
		if i == m.active {
			parts = append(parts, statusHiStyle.Render(label))
		} else {
			parts = append(parts, statusStyle.Render(label))
		}
	}
	bar := strings.Join(parts, "")
	fill := m.width - lipgloss.Width(bar)
	if fill > 0 {
		bar += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return bar
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

func (m Model) renderContent(t *tab, ln, w int) string {
	if w <= 0 || ln >= t.buf.LineCount() {
		return ""
	}
	raw := t.buf.LineAt(ln)
	exp := expandTabs(raw)
	start := t.offsetX
	if start > len(exp) {
		start = len(exp)
	}
	end := start + w
	if end > len(exp) {
		end = len(exp)
	}
	text := exp[start:end]
	if ln == t.buf.CurLine() {
		cx := visCol(raw, t.buf.Col()) - start
		if cx >= 0 && cx < len(text) {
			return string(text[:cx]) + cursorStyle.Render(string(text[cx])) + string(text[cx+1:])
		}
		if cx == len(text) {
			return string(text) + cursorStyle.Render(" ")
		}
	}
	return string(text)
}

func (m Model) statusBar() string {
	t := m.activeTab()
	base := m.baseDir()
	dirty := ""
	if t.buf.Dirty() {
		dirty = " *"
	}
	left := statusHiStyle.Render(" " + t.name(base) + dirty)
	mid := ""
	if m.msg != "" {
		mid = statusStyle.Render("  " + m.msg)
	}
	right := fmt.Sprintf("Ln %d, Col %d ", t.buf.CurLine()+1, t.buf.Col()+1)
	hint := ""
	if !m.promptOpen && !m.finderOpen {
		hint = "F1 help "
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
