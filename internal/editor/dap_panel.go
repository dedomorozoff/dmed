package editor

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"

	"dmed/internal/dap"
)

var (
	dapTitleStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	dapSelStyle    = selectionStyle
	dapScopeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true)
	dapDimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	dapColSepStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
)

// debugPanel renders the bottom debug panel: state header, threads/frames/
// variables columns, and — only when focused or holding typed text — the
// expression input row.
func (m Model) debugPanel() []string {
	h := m.debugPanelHeight()
	w := m.width
	rows := make([]string, 0, h)
	rows = append(rows, m.dapHeader(w))
	body := h - 1
	if m.dapInputVisible() {
		rows = append(rows, m.dapColumns(body-1, w)...)
		rows = append(rows, m.dapInputRow(w))
	} else {
		rows = append(rows, m.dapColumns(body, w)...)
	}
	return rows
}

// dapInputVisible reports whether the evaluator input row is shown: only while
// it holds focus or carries typed text, so the panel does not look like a
// command prompt waiting for input.
func (m Model) dapInputVisible() bool {
	return m.dapFocus == 3 || len(m.dapIn) > 0
}

func (m Model) dapHeader(w int) string {
	stateTag := "-"
	switch m.dapRunState {
	case dapLaunch:
		stateTag = "launching"
	case dapRunning:
		stateTag = "running"
	case dapStopped:
		stateTag = "stopped"
		if m.dapReason != "" {
			stateTag += " (" + m.dapReason + ")"
		}
	case dapEnded:
		stateTag = "ended"
	}
	line := statusHiStyle.Render(" DAP ") + statusStyle.Render(stateTag)
	if dapStopped == m.dapRunState && m.dapCurPath != "" {
		loc := filepath.Base(m.dapCurPath) + fmt.Sprintf(":%d", m.dapCurLine)
		line += statusStyle.Render(" " + loc)
	}
	hint := " [F4 bp] [F5 run/pause] [F6 step] [F7 in] [S+F7 out] [S+F5 stop] [Tab eval] [l console]"
	line += dapDimStyle.Render(hint)
	if fill := w - lipgloss.Width(line); fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

// dapColumnWidths splits the panel width into the threads/stack/variables
// columns. Rendering and mouse hit-testing both go through here, so a click
// always lands in the column it looks like it hit (each of the two │
// separators costs one cell).
func (m Model) dapColumnWidths(w int) (threadW, frameW, varW int) {
	const seps = 2
	threadW, frameW = 18, 34
	varW = w - threadW - frameW - seps
	if varW < 12 {
		// Narrow terminal: shrink the middle column before overflowing.
		frameW = w - threadW - seps - 12
		if frameW < 10 {
			frameW = 10
		}
		varW = w - threadW - frameW - seps
		if varW < 1 {
			varW = 1
		}
	}
	return threadW, frameW, varW
}

// dapListRows is how many list entries a column can show: the panel body minus
// the column title row and, when visible, the evaluator row.
func (m Model) dapListRows() int {
	n := m.debugPanelHeight() - 1 // panel header
	if m.dapInputVisible() {
		n--
	}
	n-- // column titles
	if n < 1 {
		n = 1
	}
	return n
}

// dapWindowStart is the first list index to render so that sel stays inside a
// window of n rows. It is a pure function of the selection and the list size,
// which is what lets the mouse map a screen row straight back to an entry.
func dapWindowStart(sel, total, n int) int {
	if n <= 0 || total <= n {
		return 0
	}
	start := sel - n/2
	if start < 0 {
		start = 0
	}
	if start > total-n {
		start = total - n
	}
	return start
}

func (m Model) dapThreadWindow(n int) int {
	return dapWindowStart(m.dapSelThread, len(m.dapThreads), n)
}

func (m Model) dapFrameWindow(n int) int {
	return dapWindowStart(m.dapSelFrame, len(m.dapFrames), n)
}

func (m Model) dapVarWindow(n int) int {
	return dapWindowStart(m.dapVarSel, len(m.dapCurrentVars()), n)
}

// dapColumns lays the three lists side by side. Focus 0 shows threads unless
// the console peek is enabled, focus 1 frames, focus 2 variables.
func (m Model) dapColumns(h, w int) []string {
	threadW, frameW, varW := m.dapColumnWidths(w)
	sep := dapColSepStyle.Render("│")
	left := m.dapThreadsCol(h, threadW)
	middle := m.dapFramesCol(h, frameW)
	right := m.dapVarsCol(h, varW)
	out := make([]string, h)
	for i := 0; i < h; i++ {
		out[i] = left[i] + sep + middle[i] + sep + right[i]
	}
	return out
}

// dapThreadsCol renders the thread list (or the console backlog when the
// console peek is toggled with 'l').
func (m Model) dapThreadsCol(h, w int) []string {
	rows := make([]string, 0, h)
	if m.dapConsolePeek {
		lines, above, below := m.dapConsoleWindow(h - 1)
		title := " console "
		if mark := consoleScrollMark(above, below); mark != "" {
			title = " console " + mark + " "
		}
		rows = append(rows, dapTitleStyle.Render(truncW(title, w)))
		if len(m.dapConsole) == 0 {
			rows = append(rows, dapDimStyle.Render(" no output yet"))
		}
		for _, l := range lines {
			rows = append(rows, truncW(l, w))
		}
		for len(rows) < h {
			rows = append(rows, "")
		}
		return rows
	}
	rows = append(rows, dapTitleStyle.Render(" threads "))
	if len(m.dapThreads) == 0 {
		if m.dapRunState == dapIdle {
			rows = append(rows, dapDimStyle.Render(" not running — F5"))
		} else {
			rows = append(rows, dapDimStyle.Render(" loading…"))
		}
	}
	for i := m.dapThreadWindow(h - 1); i < len(m.dapThreads) && len(rows) < h; i++ {
		th := m.dapThreads[i]
		rr := fmt.Sprintf("%d %s", th.ID, th.Name)
		if strings.TrimSpace(rr) == "" {
			rr = fmt.Sprintf("%d", th.ID)
		}
		if i == m.dapSelThread && m.dapFocus == 0 {
			rr = dapSelStyle.Render(rr)
		}
		rows = append(rows, truncW(rr, w))
	}
	for len(rows) < h {
		rows = append(rows, "")
	}
	return rows
}

// dapFramesCol renders the call stack of the selected thread.
func (m Model) dapFramesCol(h, w int) []string {
	rows := make([]string, 0, h)
	rows = append(rows, dapTitleStyle.Render(" stack "))
	if len(m.dapFrames) == 0 {
		rows = append(rows, dapDimStyle.Render(" —"))
	}
	for i := m.dapFrameWindow(h - 1); i < len(m.dapFrames) && len(rows) < h; i++ {
		rs := frameLabel(m.dapFrames[i])
		if i == m.dapSelFrame && m.dapFocus == 1 {
			rs = dapSelStyle.Render(rs)
		}
		rows = append(rows, truncW(rs, w))
	}
	for len(rows) < h {
		rows = append(rows, "")
	}
	return rows
}

// dapVarsCol renders the variables tree of the selected stack frame.
func (m Model) dapVarsCol(h, w int) []string {
	rows := make([]string, 0, h)
	rows = append(rows, dapTitleStyle.Render(" variables "))
	if len(m.dapVarStack) == 0 {
		if m.dapRunState == dapStopped {
			rows = append(rows, dapDimStyle.Render(" loading…"))
		} else {
			rows = append(rows, dapDimStyle.Render(" —"))
		}
	}
	vars := m.dapCurrentVars()
	for i := m.dapVarWindow(h - 1); i < len(vars) && len(rows) < h; i++ {
		v := vars[i]
		var rr string
		switch {
		case v.isScope:
			rr = "▸ " + v.name
			if i == m.dapVarSel && m.dapFocus == 2 {
				rr = dapSelStyle.Render(rr)
			} else {
				rr = dapScopeStyle.Render(rr)
			}
		case v.ref > 0:
			rr = "▸ " + v.name + " = " + v.val
			if i == m.dapVarSel && m.dapFocus == 2 {
				rr = dapSelStyle.Render(rr)
			} else {
				rr = dapDimStyle.Render("▸ ") + padVal(v.name, v.val)
			}
		default:
			rr = "  " + v.name + " = " + v.val
			if i == m.dapVarSel && m.dapFocus == 2 {
				rr = dapSelStyle.Render(rr)
			}
		}
		rows = append(rows, truncW(rr, w))
	}
	for len(rows) < h {
		rows = append(rows, "")
	}
	return rows
}

// dapInputRow is the evaluator/console-command input line. The prompt widens
// to ">>" when the input row itself is focused (Tab cycles to it); typing a
// plain character jumps here automatically.
func (m Model) dapInputRow(w int) string {
	prompt := " > "
	if m.dapFocus == 3 {
		prompt = ">> "
	}
	line := statusHiStyle.Render(prompt) + statusStyle.Render(string(m.dapIn)) + cursorStyle.Render(" ")
	if fill := w - lipgloss.Width(line); fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

// frameLabel renders "file:line func" for a stack frame.
func frameLabel(f dap.StackFrame) string {
	loc := f.Path
	if loc != "" {
		loc = filepath.Base(loc)
	}
	if f.Line > 0 {
		loc = fmt.Sprintf("%s:%d", loc, f.Line)
	}
	if f.Name != "" && f.Name != loc {
		return loc + " " + f.Name
	}
	return loc
}

func padVal(name, val string) string {
	if val == "" {
		val = "{…}"
	}
	return name + " = " + val
}

func truncW(s string, w int) string {
	r := []rune(s)
	if len(r) > w {
		return string(r[:w])
	}
	return s
}

// dapConsoleWindow returns the visible slice of the console backlog plus how
// many lines are hidden above and below it, honoring the scroll-back offset
// (0 pins the view to the newest output).
func (m Model) dapConsoleWindow(n int) (lines []string, above, below int) {
	total := len(m.dapConsole)
	if n <= 0 || total == 0 {
		return nil, 0, 0
	}
	off := m.dapConsoleScroll
	if max := total - n; max < 0 || off > max {
		if max < 0 {
			off = 0
		} else {
			off = max
		}
	}
	if off < 0 {
		off = 0
	}
	end := total - off
	start := end - n
	if start < 0 {
		start = 0
	}
	return m.dapConsole[start:end], start, total - end
}

// clampDapConsoleScroll keeps the console scroll-back offset inside the backlog.
func (m *Model) clampDapConsoleScroll() {
	max := len(m.dapConsole) - m.dapListRows()
	if max < 0 {
		max = 0
	}
	if m.dapConsoleScroll > max {
		m.dapConsoleScroll = max
	}
	if m.dapConsoleScroll < 0 {
		m.dapConsoleScroll = 0
	}
}

// consoleScrollMark labels a scrolled console title with the amount of output
// hidden above and below the window.
func consoleScrollMark(above, below int) string {
	mark := ""
	if above > 0 {
		mark += fmt.Sprintf("↑%d", above)
	}
	if below > 0 {
		mark += fmt.Sprintf("↓%d", below)
	}
	return mark
}
