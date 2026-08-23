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
)

func (m Model) viewHeight() int { return m.height - 1 }

func (m Model) gutterWidth() int {
	w := len(strconv.Itoa(m.buf.LineCount())) + 1
	if w < 4 {
		w = 4
	}
	return w
}

func (m Model) viewWidth() int { return m.width - m.gutterWidth() }

func (m Model) View() string {
	h := m.viewHeight()
	gw := m.gutterWidth()
	w := m.width - gw
	cur := m.buf.CurLine()
	rows := make([]string, 0, h+1)
	for row := 0; row < h; row++ {
		ln := m.offsetY + row
		num := strconv.Itoa(ln + 1)
		gut := strings.Repeat(" ", gw-1-len(num)) + num + " "
		if ln == cur && ln < m.buf.LineCount() {
			rows = append(rows, curGutterStyle.Render(gut)+m.renderContent(ln, w))
		} else {
			rows = append(rows, gutterStyle.Render(gut)+m.renderContent(ln, w))
		}
	}
	rows = append(rows, m.statusBar())
	return strings.Join(rows, "\n")
}

func (m Model) renderContent(ln, w int) string {
	if w <= 0 || ln >= m.buf.LineCount() {
		return ""
	}
	raw := m.buf.LineAt(ln)
	exp := expandTabs(raw)
	start := m.offsetX
	if start > len(exp) {
		start = len(exp)
	}
	end := start + w
	if end > len(exp) {
		end = len(exp)
	}
	text := exp[start:end]
	if ln == m.buf.CurLine() {
		cx := visCol(raw, m.buf.Col()) - start
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
	dirty := ""
	if m.buf.Dirty() {
		dirty = " *"
	}
	left := statusHiStyle.Render(" " + m.path + dirty)
	mid := ""
	if m.msg != "" {
		mid = statusStyle.Render("  " + m.msg)
	}
	right := statusStyle.Render(fmt.Sprintf("Ln %d, Col %d ", m.buf.CurLine()+1, m.buf.Col()+1))
	fill := m.width - lipgloss.Width(left) - lipgloss.Width(mid) - lipgloss.Width(right)
	if fill > 0 {
		return left + mid + statusStyle.Render(strings.Repeat(" ", fill)) + right
	}
	return left + mid + right
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
