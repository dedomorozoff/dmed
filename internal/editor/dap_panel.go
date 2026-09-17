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
// variables columns, and the expression input row.
func (m Model) debugPanel() []string {
	h := m.debugPanelHeight()
	w := m.width
	rows := make([]string, 0, h)
	rows = append(rows, m.dapHeader(w))
	rows = append(rows, m.dapColumns(h-2, w)...)
	rows = append(rows, m.dapInputRow(w))
	return rows
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
	hint := " [F4 bp] [F5 run] [F10 step] [F11 in] [S+F11 out] [S+F5 stop] [Tab lists] [l console]"
	line += dapDimStyle.Render(hint)
	if fill := w - lipgloss.Width(line); fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

// dapColumns lays the three lists side by side. Focus 0 shows threads unless
// the console peek is enabled, focus 1 frames, focus 2 variables.
func (m Model) dapColumns(h, w int) []string {
	threadW := 18
	frameW := 34
	sep := dapColSepStyle.Render("│")
	varW := w - threadW - frameW - lipgloss.Width(sep)*2
	if varW < 12 {
		varW = 12
	}
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
		rows = append(rows, dapTitleStyle.Render(" console "))
		if len(m.dapConsole) == 0 {
			rows = append(rows, dapDimStyle.Render(" no output yet"))
		}
		for _, l := range m.lastConsoleLines(h - 1) {
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
	for i, th := range m.dapThreads {
		rr := fmt.Sprintf("%d %s", th.ID, th.Name)
		if strings.TrimSpace(rr) == "" {
			rr = fmt.Sprintf("%d", th.ID)
		}
		if i == m.dapSelThread && m.dapFocus == 0 && !m.dapConsolePeek {
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
	for i, f := range m.dapFrames {
		rs := frameLabel(f)
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
	for i, v := range vars {
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

// dapInputRow is the evaluator/console-command input line.
func (m Model) dapInputRow(w int) string {
	line := statusHiStyle.Render(" > ") + statusStyle.Render(string(m.dapIn)) + cursorStyle.Render(" ")
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

func (m Model) lastConsoleLines(n int) []string {
	if n <= 0 {
		return nil
	}
	if len(m.dapConsole) <= n {
		return m.dapConsole
	}
	return m.dapConsole[len(m.dapConsole)-n:]
}
