package editor

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"dmed/internal/syntax"
	"dmed/internal/vcs"
)

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
	gitAddStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	gitModStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	gitDelStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	diffAddBg       = lipgloss.NewStyle().Background(lipgloss.Color("22"))
	diffDelBg       = lipgloss.NewStyle().Background(lipgloss.Color("52"))
	diffModBg       = lipgloss.NewStyle().Background(lipgloss.Color("58"))
	selectionStyle  = lipgloss.NewStyle().Background(lipgloss.Color("60")).Foreground(lipgloss.Color("255"))
)

type helpEntry struct {
	keys string
	desc string
}

var helpEntries = []helpEntry{
	{"Ctrl+S", "save active tab (untitled: Save As)"},
	{"", ""},
	{"Ctrl+P / F2", "Command Palette (File: New, Save, ...)"},
	{"Shift+Arrows", "select text range"},
	{"Ctrl+C / Ctrl+X / Ctrl+V", "copy / cut / paste"},
	{"", ""},
	{"Ctrl+F", "search in file (Enter/F3 next, Shift+F3 prev)"},
	{"Ctrl+H", "search & replace (Tab switch, Enter rep, Ctrl+A all)"},
	{"Ctrl+G", "Git panel; Ctrl+B switches back to tree"},
	{"D (in Git panel)", "side-by-side diff vs HEAD"},
	{"Alt+[ / Alt+]", "jump to previous / next Git hunk"},
	{"Ctrl+O", "fuzzy file finder"},
	{"Ctrl+T", "open file by path"},
	{"Alt+T", "toggle bottom terminal (Esc closes)"},
	{"Alt+A", "AI chat panel (local Ollama, right side)"},
	{"Alt+I", "AI inline rewrite (select text, describe change)"},
	{"Alt+L", "background agent tasks panel (queue, progress, cancel)"},
	{"Ctrl+B / F9", "project tree; Ctrl+G switches to Git"},
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
	{"Ctrl+Z / Ctrl+R", "undo / redo"},
	{"Ctrl+Y / Ctrl+D", "delete line / duplicate line"},
	{"Alt+D", "multi-cursor: add cursor at next occurrence of word"},
	{"Alt+Click", "add cursor at click position"},
	{"Esc", "exit multi-cursor mode"},
	{"Alt+↑ / Alt+↓", "move line up / down"},
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

func (m Model) paletteExtraRows() int {
	if !m.paletteOpen {
		return 0
	}
	hits := m.filterPalette()
	if len(hits) > 8 {
		return 9
	}
	return len(hits) + 1
}

func (m Model) termPanelHeight() int {
	h := m.height / 3
	if h < 6 {
		h = 6
	}
	if h > 16 {
		h = 16
	}
	return h
}

func (m Model) termExtraRows() int {
	if !m.termOpen {
		return 0
	}
	return m.termPanelHeight()
}

func (m Model) viewHeight() int {
	h := m.height - 2 - m.finderExtraRows() - m.paletteExtraRows() - m.termExtraRows()
	if h < 1 {
		h = 1
	}
	return h
}

func (m Model) gutterWidthForTab(t *tab) int {
	w := len(strconv.Itoa(t.buf.LineCount())) + 2
	if w < 5 {
		w = 5
	}
	return w
}

func (m Model) paneContentWidth(paneIdx int) int {
	t := &m.tabs[m.panes[paneIdx].tabIdx]
	return m.paneTotalWidth(paneIdx) - m.gutterWidthForTab(t)
}

func (m Model) View() tea.View {
	h := m.viewHeight()
	rows := make([]string, 0, h+2)
	rows = append(rows, m.tabBar())
	if m.diffViewOpen {
		rows = append(rows, m.diffViewRows(h)...)
	} else if m.gitOpen && (m.gitMode == gitModeStatus || m.gitMode == gitModeLog) && len(m.diffRows) > 0 {
		// Inline diff preview: show side-by-side diff of selected file/commit
		// in the editor area while the git panel is open.
		diffRows := renderSideBySide(m.diffHeadLines, m.diffRightLines, m.diffRows, m.diffOffsetY, m.diffOffsetX, m.editorAreaWidth(), h, m.diffHeadSyntax, m.diffRightSyntax)
		rows = append(rows, m.composeSidebar(diffRows)...)
	} else if m.aiReviewMode {
		rows = append(rows, renderSideBySide(m.aiReviewLeft, m.aiReviewRight, m.aiReviewRows, m.aiReviewOffY, m.aiReviewOffX, m.width, h, nil, nil)...)
	} else if m.agentReviewMode {
		rows = append(rows, renderSideBySide(m.agentReviewLeft, m.agentReviewRight, m.agentReviewRows, m.agentReviewOffY, m.agentReviewOffX, m.width, h, nil, nil)...)
	} else if m.conflictOpen && len(m.conflictRows) > 0 {
		rows = append(rows, renderSideBySide(m.conflictLeftLines, m.conflictRightLines, m.conflictRows, m.conflictOffY, m.conflictOffX, m.width, h, nil, nil)...)
	} else if m.aiCfgOpen {
		rows = append(rows, m.aiSettingsPanel(h)...)
	} else if m.helpOpen {
		rows = append(rows, m.helpPanel(h)...)
	} else {
		rows = append(rows, m.editorRows(h)...)
	}
	bottom := m.statusBar()
	if m.diffViewOpen {
		bottom = m.diffBottom()
	} else if m.gitOpen && (m.gitMode == gitModeStatus || m.gitMode == gitModeLog) && len(m.diffRows) > 0 {
		bottom = m.diffBottom()
	} else if m.aiReviewMode {
		bottom = m.aiReviewBottom()
	} else if m.agentReviewMode {
		bottom = m.agentReviewBottom()
	} else if m.agentPrompt {
		bottom = m.agentPromptLine()
	} else if m.aiInlineOpen {
		bottom = m.aiInlinePrompt()
	} else if m.aiInlineBusy {
		bottom = m.aiInlineBusyLine()
	} else if m.conflictOpen {
		bottom = m.conflictLine()
	} else if m.quitConfirm {
		bottom = m.quitLine()
	} else if m.aiCfgOpen {
		if m.aiCfgEdit {
			bottom = m.aiCfgEditLine()
		} else {
			bottom = m.aiCfgBottom()
		}
	} else if m.gitOpen {
		if m.gitMode == gitModeCommit {
			bottom = m.gitLine()
		} else if m.gitMode == gitModeLog {
			bottom = m.gitLogStatusLine()
		} else if m.gitMode == gitModeBranch {
			bottom = m.gitBranchLine()
		} else {
			bottom = m.gitStatusLine()
		}
	} else if m.promptSave {
		bottom = m.saveLine()
	} else if m.promptOpen {
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
	if m.paletteOpen {
		rows = append(rows, m.palettePanel()...)
	}
	if m.termOpen {
		rows = append(rows, m.terminalPanel()...)
	}
	var v tea.View
	v.SetContent(lipgloss.NewStyle().MaxWidth(m.width).Render(strings.Join(rows, "\n")))
	v.AltScreen = true
	v.WindowTitle = "dmed — " + m.activeTab().name(m.baseDir())
	v.MouseMode = tea.MouseModeCellMotion

	// Terminal cursor: positioned at the editor cursor location.
	if !m.gitOpen && !m.agentOpen && !m.agentReviewMode && !m.paletteOpen && !m.helpOpen && !m.aiCfgOpen && !m.searchOpen && !m.promptOpen && !m.termOpen && !m.chatOpen {
		cx, cy := m.cursorScreenPos()
		v.Cursor = tea.NewCursor(cx, cy)
	}

	return v
}

func (m Model) editorRows(h int) []string {
	var rows []string
	if m.layout == splitNone {
		rows = m.composeSidebar(m.renderPaneRows(0, h, m.paneTotalWidth(0)))
		return m.composeChatRail(rows)
	} else if m.layout == splitVert {
		w0 := m.paneTotalWidth(0)
		w1 := m.paneTotalWidth(1)
		left := m.renderPaneRows(0, h, w0)
		right := m.renderPaneRows(1, h, w1)
		combined := make([]string, h)
		sepColor := "238"
		if m.activePane == 0 {
			sepColor = "61" // highlight left pane separator
		}
		sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(sepColor))
		sep := sepStyle.Render("│")
		for row := 0; row < h; row++ {
			combined[row] = padTo(left[row], w0) + sep + padTo(right[row], w1)
		}
		rows = m.composeSidebar(combined)
		return m.composeChatRail(rows)
	}
	// splitHoriz
	h0 := m.paneViewHeight(0)
	h1 := m.paneViewHeight(1)
	w0 := m.paneTotalWidth(0)
	w1 := m.paneTotalWidth(1)
	top := m.renderPaneRows(0, h0, w0)
	bottom := m.renderPaneRows(1, h1, w1)
	for row := range top {
		top[row] = padTo(top[row], w0)
	}
	for row := range bottom {
		bottom[row] = padTo(bottom[row], w1)
	}
	sepColor := "238"
	if m.activePane == 0 {
		sepColor = "61" // highlight top pane separator
	}
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(sepColor))
	sep := sepStyle.Render(strings.Repeat("─", m.editorAreaWidth()))
	combined := make([]string, 0, h0+h1+1)
	combined = append(combined, top...)
	combined = append(combined, sep)
	combined = append(combined, bottom...)
	rows = m.composeSidebar(combined)
	return m.composeChatRail(rows)
}

// composeChatRail appends the AI chat panel to the right of every editor
// row. It is a no-op while the panel is closed.
func (m Model) composeChatRail(editor []string) []string {
	if !m.chatOpen {
		return editor
	}
	w := m.chatPanelWidth()
	panel := m.chatPanel(len(editor))
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	if m.chatFocus {
		sepStyle = sepStyle.Foreground(lipgloss.Color("61"))
	}
	sep := sepStyle.Render("│")
	out := make([]string, len(editor))
	for row, line := range editor {
		out[row] = padTo(line, m.width-w-1) + sep + panel[row]
	}
	return out
}

func (m Model) composeSidebar(editor []string) []string {
	var rail []string
	switch {
	case m.agentOpen:
		rail = m.agentPanel(len(editor))
	case m.gitOpen:
		rail = m.gitPanel(len(editor))
	case m.sidebarOn():
		rail = m.treePanel(len(editor))
	default:
		return editor
	}
	out := make([]string, len(editor))
	for row := range editor {
		out[row] = rail[row] + editor[row]
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
	diff := t.getDiff(m.repo)

	for row := 0; row < h; row++ {
		ln := p.offsetY + row
		num := strconv.Itoa(ln + 1)
		gitMark := " "
		gitMarkStyle := gutterStyle
		if ln < len(diff.Lines) {
			switch diff.Lines[ln] {
			case vcs.DiffAdded:
				gitMark = "+"
				gitMarkStyle = gitAddStyle
			case vcs.DiffModified:
				gitMark = "~"
				gitMarkStyle = gitModStyle
			case vcs.DiffDeleted:
				gitMark = "_"
				gitMarkStyle = gitDelStyle
			}
		}

		numPad := gw - 2 - len(num)
		if numPad < 0 {
			numPad = 0
		}
		numStr := strings.Repeat(" ", numPad) + num + " "
		gutStr := numStr
		if active && ln == cur && ln < t.buf.LineCount() {
			gutStr = curGutterStyle.Render(numStr)
		} else {
			gutStr = gutterStyle.Render(numStr)
		}
		gutStr += gitMarkStyle.Render(gitMark)

		if ln < t.buf.LineCount() {
			rows[row] = gutStr + m.renderLine(p, t, ln, contentW, active, syntaxLines)
		} else {
			rows[row] = gutStr + m.renderLine(p, t, ln, contentW, active, syntaxLines)
		}
	}
	return rows
}

// gitPanelWidth is the width of the left Git rail (like the project tree).
const gitPanelWidth = 30

func (m Model) sidebarWidth() int {
	if m.sidebarOn() {
		return m.cfg.UI.TreeWidth
	}
	return 0
}

// leftRailWidth is the width of the whole left column: the Git panel takes
// precedence over the project tree while it is open.
func (m Model) leftRailWidth() int {
	if m.gitOpen || m.agentOpen {
		return gitPanelWidth
	}
	return m.sidebarWidth()
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
	inner := m.cfg.UI.TreeWidth - 2
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
					label = "- " + label
				} else {
					label = "+ " + label
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

func padTo(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
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
	label := " open file: "
	if m.promptNewFile {
		label = " new file: "
	} else if m.promptNewFolder {
		label = " new folder: "
	}
	line := statusHiStyle.Render(label) + statusStyle.Render(string(m.promptIn)) + cursorStyle.Render(" ")
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

func (m Model) saveLine() string {
	line := statusHiStyle.Render(" save as: ") + statusStyle.Render(string(m.promptSaveIn)) + cursorStyle.Render(" ")
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

func (m Model) quitLine() string {
	line := statusHiStyle.Render(" save changes? ") + statusStyle.Render("(Y)es / (N)o / (Esc) Cancel")
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

func (m Model) gitLine() string {
	line := statusHiStyle.Render(" git commit: ") + statusStyle.Render(string(m.gitCommitIn)) + cursorStyle.Render(" ")
	if m.repo != nil {
		branch := m.repo.Branch()
		if branch != "" {
			line += hintStyle.Render(fmt.Sprintf(" (%s: %s)", branch, m.repo.StatusSummary()))
		}
	}
	line += hintStyle.Render("  (Enter: commit, Esc: close)")
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

func (m Model) gitStatusLine() string {
	r := m.repoForCur()
	var hint string
	if r == nil {
		hint = "(i: init repo, esc/q: close)"
	} else {
		hint = "(space: stage, a: all, c: commit, d: diff, l: log, b: branch, r: refresh, q: close)"
	}
	line := ""
	if r == nil {
		line = statusHiStyle.Render(" git: ") + statusStyle.Render(" no repository")
	} else {
		summary := r.StatusSummary()
		staged := 0
		for _, fs := range m.gitFiles {
			if fs.IsStaged() {
				staged++
			}
		}
		line = statusHiStyle.Render(" git ") +
			hintStyle.Render("("+r.Branch()+" "+summary+")") +
			statusStyle.Render(fmt.Sprintf(" %d changed, %d staged", len(m.gitFiles), staged))
	}
	// The hint must stay visible even on narrow terminals: if the summary
	// doesn't leave room for it on a single status line, trim the summary.
	fill := m.width - lipgloss.Width(hint) - lipgloss.Width(line)
	if fill < 0 {
		line = statusStyle.Render(fitStatusTail(line, m.width-lipgloss.Width(hint)))
		fill = m.width - lipgloss.Width(hint) - lipgloss.Width(line)
	}
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line + hintStyle.Render(hint)
}

// fitStatusTail strips styling and keeps the tail of s within w visible columns.
func fitStatusTail(s string, w int) string {
	r := []rune(stripANSI(s))
	if len(r) <= w {
		return s
	}
	if w < 1 {
		return ""
	}
	return "…" + string(r[len(r)-(w-1):])
}

func (m Model) gitLogStatusLine() string {
	var line string
	if len(m.gitLogEntries) == 0 {
		line = statusHiStyle.Render(" LOG ") + statusStyle.Render(" no commits")
	} else {
		line = statusHiStyle.Render(" LOG ") + hintStyle.Render(fmt.Sprintf("(%d commits)", len(m.gitLogEntries)))
	}
	hint := "(j/k: navigate, Tab: diff focus, esc/q: back, r: refresh)"
	fill := m.width - lipgloss.Width(line) - lipgloss.Width(hint)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line + hintStyle.Render(hint)
}

func (m Model) gitBranchLine() string {
	if m.gitBranchNew {
		line := statusHiStyle.Render(" new branch: ") + statusStyle.Render(string(m.gitBranchIn)) + cursorStyle.Render(" ")
		line += hintStyle.Render("  (Enter: create, Esc: cancel)")
		fill := m.width - lipgloss.Width(line)
		if fill > 0 {
			line += statusStyle.Render(strings.Repeat(" ", fill))
		}
		return line
	}
	var line string
	if r := m.repoForCur(); r != nil {
		line = statusHiStyle.Render(" BRANCH ") + statusStyle.Render(r.Branch())
	}
	hint := "(j/k: switch, Enter: checkout, n: new branch, esc/q: back, r: refresh)"
	fill := m.width - lipgloss.Width(line) - lipgloss.Width(hint)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line + hintStyle.Render(hint)
}

// fitPath keeps the tail of long paths (the file name matters most).
func fitPath(p string, w int) string {
	r := []rune(p)
	if len(r) <= w {
		return p
	}
	if w < 1 {
		return ""
	}
	return "…" + string(r[len(r)-(w-1):])
}

func (m Model) gitPanel(h int) []string {
	if m.gitMode == gitModeLog {
		return m.gitLogPanel(h)
	}
	if m.gitMode == gitModeBranch {
		return m.branchPanel(h)
	}
	rows := make([]string, 0, h)
	start := m.gitOffset
	end := start + m.gitListHeight()
	for i := start; i < end && len(rows) < h; i++ {
		fs := m.gitFiles[i]
		marker := fmt.Sprintf("%c%c", fs.Staging, fs.Worktree)
		path := fitPath(fs.Path, gitPanelWidth-5)
		plain := " " + marker + " " + path
		pad := gitPanelWidth - 1 - lipgloss.Width(plain)
		if pad < 0 {
			pad = 0
		}
		line := plain + strings.Repeat(" ", pad)
		var cell string
		switch {
		case i == m.gitSel:
			cell = statusHiStyle.Render(line)
		case fs.IsStaged():
			styled := " " + gitAddStyle.Render(marker) + " " + path
			cell = styled + strings.Repeat(" ", maxInt(0, gitPanelWidth-1-lipgloss.Width(styled)))
		case fs.Worktree == vcs.StatusUntracked:
			styled := " " + hintStyle.Render(marker) + " " + path
			cell = styled + strings.Repeat(" ", maxInt(0, gitPanelWidth-1-lipgloss.Width(styled)))
		default:
			styled := " " + gitModStyle.Render(marker) + " " + path
			cell = styled + strings.Repeat(" ", maxInt(0, gitPanelWidth-1-lipgloss.Width(styled)))
		}
		rows = append(rows, cell+" ")
	}
	for len(rows) < h {
		rows = append(rows, strings.Repeat(" ", gitPanelWidth))
	}
	return rows
}

func (m Model) gitLogPanel(h int) []string {
	rows := make([]string, 0, h)
	start := m.gitLogOffset
	end := start + m.gitLogListHeight()
	for i := start; i < end && len(rows) < h; i++ {
		entry := m.gitLogEntries[i]
		// Two-line entry: " hash  subject" on first line, "         author time" on second
		hashStr := entry.Hash
		subject := fitPath(entry.Subject, gitPanelWidth-lipgloss.Width(hashStr)-3)
		first := " " + gitAddStyle.Render(hashStr) + " " + subject
		// Pad first line
		visW := lipgloss.Width(first)
		if pad := gitPanelWidth - 1 - visW; pad > 0 {
			first += strings.Repeat(" ", pad)
		}
		// Second line: relative time + author
		relTime := entry.When.Format("Jan 02 15:04")
		if len(entry.Author) > 10 {
			entry.Author = entry.Author[:10]
		}
		second := " " + hintStyle.Render(entry.Author+" "+relTime)
		visW2 := lipgloss.Width(second)
		if pad := gitPanelWidth - 1 - visW2; pad > 0 {
			second += strings.Repeat(" ", pad)
		}

		if i == m.gitLogSel {
			rows = append(rows, statusHiStyle.Render(first))
			rows = append(rows, statusHiStyle.Render(second))
		} else {
			rows = append(rows, first)
			rows = append(rows, second)
		}
	}
	for len(rows) < h {
		rows = append(rows, strings.Repeat(" ", gitPanelWidth))
	}
	return rows
}

// diffViewRows renders the side-by-side HEAD vs buffer view: left column is
// the old text, right column the current text. The diff rail is drawn over
// the full editor width (no sidebar while the diff is open).
func (m Model) diffViewRows(h int) []string {
	return renderSideBySide(m.diffHeadLines, m.diffRightLines, m.diffRows, m.diffOffsetY, m.diffOffsetX, m.width, h, m.diffHeadSyntax, m.diffRightSyntax)
}

// renderSideBySide renders a two-column diff view. Used by git diff, AI inline
// review, and conflict preview. Pass nil for leftSyntax/rightSyntax to disable
// syntax highlighting.
func renderSideBySide(leftLines, rightLines []string, diffRows []vcs.DiffRow, offsetY, offsetX, w, h int, leftSyntax, rightSyntax []syntax.HighlightedLine) []string {
	half := (w - 1) / 2

	numW := len(strconv.Itoa(maxInt(len(leftLines), len(rightLines)))) + 1
	if numW < 3 {
		numW = 3
	}
	contentW := half - numW - 2 // number, space, marker, space
	if contentW < 1 {
		contentW = 1
	}

	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	sep := sepStyle.Render("│")

	rows := make([]string, h)
	for row := 0; row < h; row++ {
		idx := offsetY + row
		var left, right string
		if idx < len(diffRows) {
			dr := diffRows[idx]
			var ls, rs syntax.HighlightedLine
			if leftSyntax != nil && idx < len(leftSyntax) {
				ls = leftSyntax[idx]
			}
			if rightSyntax != nil && idx < len(rightSyntax) {
				rs = rightSyntax[idx]
			}
			left = renderDiffCell(leftLines, dr.Left, dr.Type, false, numW, contentW, offsetX, ls)
			right = renderDiffCell(rightLines, dr.Right, dr.Type, true, numW, contentW, offsetX, rs)
		} else {
			left = strings.Repeat(" ", half)
			right = strings.Repeat(" ", half)
		}
		rows[row] = padTo(left+sep+right, w)
	}
	return rows
}

func renderDiffCell(lines []string, lineIdx int, dt vcs.DiffType, rightSide bool, numW, contentW, offsetX int, syntaxLine syntax.HighlightedLine) string {
	half := numW + 2 + contentW
	if lineIdx < 0 {
		return strings.Repeat(" ", half)
	}
	text := ""
	var runes []rune
	if lineIdx < len(lines) {
		runes = []rune(lines[lineIdx])
		lo := offsetX
		if lo > len(runes) {
			lo = len(runes)
		}
		hi := lo + contentW
		if hi > len(runes) {
			hi = len(runes)
		}
		runes = runes[lo:hi]
		text = string(runes)
	}
	marker := ' '
	var bg *lipgloss.Style
	switch dt {
	case vcs.DiffAdded:
		if rightSide {
			marker = '+'
			bg = &diffAddBg
		}
	case vcs.DiffDeleted:
		if !rightSide {
			marker = '-'
			bg = &diffDelBg
		}
	case vcs.DiffModified:
		marker = '~'
		bg = &diffModBg
	}

	// Build the prefix: line number + marker + space
	prefix := fmt.Sprintf("%*d %c ", numW-1, lineIdx+1, marker)
	needed := half - lipgloss.Width(prefix)
	if needed < 0 {
		needed = 0
	}

	if syntaxLine != nil || bg != nil {
		var out strings.Builder
		out.WriteString(prefix)
		for i := 0; i < len(runes) && i < needed; i++ {
			var st lipgloss.Style
			if syntaxLine != nil && i+offsetX < len(syntaxLine) {
				st = syntaxLine[i+offsetX]
			}
			if bg != nil {
				st = st.Background(bg.GetBackground())
			}
			out.WriteString(st.Render(string(runes[i])))
		}
		// Pad remaining space
		visW := lipgloss.Width(out.String())
		if pad := half - visW; pad > 0 {
			if bg != nil {
				out.WriteString(bg.Render(strings.Repeat(" ", pad)))
			} else {
				out.WriteString(strings.Repeat(" ", pad))
			}
		}
		return out.String()
	}

	cell := prefix + text
	if pad := half - len([]rune(cell)); pad > 0 {
		cell += strings.Repeat(" ", pad)
	}
	return cell
}

func (m Model) diffBottom() string {
	added, modified, deleted := 0, 0, 0
	for _, dr := range m.diffRows {
		switch dr.Type {
		case vcs.DiffAdded:
			added++
		case vcs.DiffModified:
			modified++
		case vcs.DiffDeleted:
			deleted++
		}
	}
	hint := " Space stage  c commit  a stage-all  r refresh  d full-diff  l log  Tab diff"
	if m.gitDiffFocused {
		hint = " j/k scroll  h/l h-scroll  Tab/Esc back"
	} else if m.gitMode == gitModeLog {
		hint = " j/k commits  Tab diff  Esc files  r refresh"
	}
	modeTag := ""
	if m.gitMode == gitModeLog {
		modeTag = statusHiStyle.Render(" LOG ") + " "
	}
	line := modeTag + statusHiStyle.Render(" diff ") + statusStyle.Render(m.diffPath) +
		hintStyle.Render(fmt.Sprintf("  +%d ~%d -%d", added, modified, deleted)) +
		hintStyle.Render(hint)
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

func (m Model) terminalPanel() []string {
	h := m.termPanelHeight()
	w := m.width
	rows := make([]string, 0, h)

	outH := h - 1 // last row is the input line
	lines := m.termLines
	start := len(lines) - outH + m.termScroll
	if start < 0 {
		start = 0
	}
	end := start + outH
	if end > len(lines) {
		end = len(lines)
		if start > len(lines)-outH {
			start = maxInt(0, len(lines)-outH)
		}
	}
	for i := start; i < end; i++ {
		r := []rune(stripANSI(lines[i]))
		if len(r) > w {
			r = r[len(r)-w:] // keep the tail of long lines
		}
		rows = append(rows, string(r))
	}
	for len(rows) < outH {
		rows = append(rows, "")
	}

	input := statusHiStyle.Render(" > ") + statusStyle.Render(string(m.termIn)) + cursorStyle.Render(" ")
	fill := w - lipgloss.Width(input)
	if fill > 0 {
		input += statusStyle.Render(strings.Repeat(" ", fill))
	}
	rows = append(rows, input)
	return rows
}

// chatPanel renders the right-side AI chat rail: header with the model
// name, scrolling transcript, input line at the bottom.
func (m Model) chatPanel(h int) []string {
	w := m.chatPanelWidth()
	rows := make([]string, 0, h)

	model := m.chatModel
	if model == "" {
		model = "no model"
	}
	header := statusHiStyle.Render(fmt.Sprintf(" AI · %s ", fitPath(model, w-6)))
	if m.chatBusy {
		header += statusStyle.Render(" streaming ")
	}
	if fill := w - lipgloss.Width(header); fill > 0 {
		header += statusStyle.Render(strings.Repeat(" ", fill))
	}
	rows = append(rows, header)

	bodyH := h - 2 // header + input line
	total := len(m.chatRows)
	start := total - bodyH + m.chatScroll
	if start > total-bodyH {
		start = total - bodyH
	}
	if start < 0 {
		start = 0
	}
	for i := start; i < start+bodyH; i++ {
		cell := strings.Repeat(" ", w)
		if i >= 0 && i < total {
			var st lipgloss.Style
			switch m.chatRows[i].kind {
			case "label-you":
				st = chatUserLabelStyle
			case "user":
				st = chatUserTextStyle
			case "label-ai":
				st = chatAILabelStyle
			case "ai":
				st = chatAITextStyle
			case "err":
				st = gitDelStyle
			default:
				st = hintStyle
			}
			line := st.Render(m.chatRows[i].text)
			cell = line + strings.Repeat(" ", maxInt(0, w-lipgloss.Width(line)))
		}
		rows = append(rows, cell)
	}

	input := statusHiStyle.Render(" ❯ ") + statusStyle.Render(string(m.chatIn)) + cursorStyle.Render(" ")
	if m.chatBusy {
		input += hintStyle.Render("⋯")
	}
	if fill := w - lipgloss.Width(input); fill > 0 {
		input += statusStyle.Render(strings.Repeat(" ", fill))
	}
	rows = append(rows, input)
	return rows
}

func (m Model) conflictLine() string {
	fname := filepath.Base(m.conflictPath)
	line := statusHiStyle.Render(" CONFLICT ") + statusStyle.Render(fmt.Sprintf(" File modified on disk: [R]eload / [I]gnore? (%s)", fname))
	if len(m.conflictRows) > 0 {
		added, modified, deleted := 0, 0, 0
		for _, dr := range m.conflictRows {
			switch dr.Type {
			case vcs.DiffAdded:
				added++
			case vcs.DiffModified:
				modified++
			case vcs.DiffDeleted:
				deleted++
			}
		}
		line += hintStyle.Render(fmt.Sprintf("  +%d ~%d -%d", added, modified, deleted))
		line += hintStyle.Render("  (↑↓ scroll)")
	}
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

func (m Model) aiInlinePrompt() string {
	line := statusHiStyle.Render(" AI instruction: ") + statusStyle.Render(string(m.aiInlineInput)) + cursorStyle.Render(" ")
	hint := "  (Enter: submit, Esc: cancel)"
	line += hintStyle.Render(hint)
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

func (m Model) aiInlineBusyLine() string {
	preview := m.aiInlineProposal
	if len(preview) > 60 {
		preview = preview[:60] + "..."
	}
	line := statusHiStyle.Render(" AI thinking... ") + hintStyle.Render(preview)
	hint := "  (Esc to cancel)"
	line += hintStyle.Render(hint)
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

func (m Model) aiReviewBottom() string {
	added, modified, deleted := 0, 0, 0
	for _, dr := range m.aiReviewRows {
		switch dr.Type {
		case vcs.DiffAdded:
			added++
		case vcs.DiffModified:
			modified++
		case vcs.DiffDeleted:
			deleted++
		}
	}
	line := statusHiStyle.Render(" AI diff ") +
		hintStyle.Render(fmt.Sprintf("  +%d ~%d -%d", added, modified, deleted)) +
		hintStyle.Render("  (y: accept, n: reject, ↑↓ scroll)")
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
			for k := 0; k < m.cfg.Editor.TabWidth; k++ {
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

	// All cursor positions and per-cursor selections on this line (active pane
	// only). Secondary cursors render as reverse cells; their selections use
	// the normal selection style.
	var carets []int
	var selRanges [][2]int
	if activePane {
		for _, c := range t.buf.Cursors() {
			if c.Line != ln {
				continue
			}
			if c.Col <= len(raw) {
				carets = append(carets, rawToExp[c.Col])
			} else if len(raw) >= 0 {
				carets = append(carets, rawToExp[len(raw)])
			}
			if c.From != c.To {
				cf, ct := c.From, c.To
				if cf > len(raw) {
					cf = len(raw)
				}
				if ct > len(raw) {
					ct = len(raw)
				}
				selRanges = append(selRanges, [2]int{rawToExp[cf], rawToExp[ct]})
			}
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

	// Selection range
	selStart := -1
	selEnd := -1
	if t.buf.HasSelection() {
		sl, sc, el, ec := t.buf.SelectionRange()
		if ln >= sl && ln <= el {
			if ln == sl {
				if sc <= len(raw) {
					selStart = rawToExp[sc]
				}
			} else {
				selStart = 0
			}

			if ln == el {
				if ec <= len(raw) {
					selEnd = rawToExp[ec]
				}
			} else {
				selEnd = len(exp)
			}
		}
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

		if selStart >= 0 && i >= selStart && i < selEnd {
			st = selectionStyle
		} else {
			for _, rng := range selRanges {
				if i >= rng[0] && i < rng[1] {
					st = selectionStyle
					break
				}
			}
		}

		for _, cc := range carets {
			if i == cc {
				st = cursorStyle
				break
			}
		}

		out.WriteString(st.Render(string(r)))
	}

	// Cursor(s) at end of line
	for _, cc := range carets {
		if cc == len(exp) && cc >= start && cc < start+w {
			out.WriteString(cursorStyle.Render(" "))
		}
	}

	return out.String()
}

func (m Model) palettePanel() []string {
	const paletteVisible = 8
	hits := m.filterPalette()
	displayHits := hits
	total := len(displayHits)
	if total > paletteVisible {
		if m.paletteOffset > total-paletteVisible {
			m.paletteOffset = total - paletteVisible
		}
		if m.paletteOffset < 0 {
			m.paletteOffset = 0
		}
		displayHits = displayHits[m.paletteOffset : m.paletteOffset+paletteVisible]
	}
	rows := make([]string, 0, len(displayHits)+1)
	for i, hit := range displayHits {
		label := fmt.Sprintf(" %s — %s ", hit.title, hit.desc)
		if m.paletteOffset+i == m.paletteSel {
			rows = append(rows, statusHiStyle.Render(label))
		} else {
			rows = append(rows, statusStyle.Render(label))
		}
	}
	line := statusHiStyle.Render(" > ") + statusStyle.Render(string(m.paletteQ)) + cursorStyle.Render(" ")
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	rows = append(rows, line)
	return rows
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
	if m.repo != nil {
		b := m.repo.Branch()
		if b != "" {
			left += hintStyle.Render(" (" + b + ")")
		}
	}

	mid := ""
	if m.msg != "" {
		mid = statusStyle.Render("  " + m.msg)
	}
	right := fmt.Sprintf("Ln %d, Col %d ", t.buf.CurLine()+1, t.buf.Col()+1)
	fileInfo := ""
	if t.path != "" {
		endings := map[string]string{"lf": "LF", "crlf": "CRLF"}
		enc := strings.ToUpper(t.encoding)
		fileInfo = fmt.Sprintf("%s %s ", endings[t.lineEnding], enc)
	}
	hint := ""
	if !m.promptOpen && !m.promptSave && !m.quitConfirm && !m.finderOpen && !m.searchOpen && !m.gitOpen && !m.conflictOpen && !m.diffViewOpen && !m.termOpen && !m.chatOpen && !m.aiInlineOpen && !m.aiInlineBusy && !m.aiReviewMode && !m.aiCfgOpen && !m.helpOpen && !m.agentOpen && !m.agentReviewMode {
		hint = "F1 help "
		if m.layout != splitNone {
			hint += "F8 pane "
		}
	}
	rightBar := hintStyle.Render(hint) + statusStyle.Render(fileInfo) + statusStyle.Render(right)
	fill := m.width - lipgloss.Width(left) - lipgloss.Width(mid) - lipgloss.Width(rightBar)
	if fill > 0 {
		return left + mid + statusStyle.Render(strings.Repeat(" ", fill)) + rightBar
	}
	return left + mid + rightBar
}

func expandTabs(l []rune, tw int) []rune {
	out := make([]rune, 0, len(l))
	for _, r := range l {
		if r == '\t' {
			for i := 0; i < tw; i++ {
				out = append(out, ' ')
			}
		} else {
			out = append(out, r)
		}
	}
	return out
}

func visCol(l []rune, col int, tw int) int {
	x := 0
	for _, r := range l[:col] {
		if r == '\t' {
			x += tw
		} else {
			x++
		}
	}
	return x
}
