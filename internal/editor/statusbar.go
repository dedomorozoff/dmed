package editor

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"dmed/internal/syntax"
)

// Clickable status-bar icons. The bottom-left of the status bar carries a
// compact strip of single-width Unicode glyphs that toggle the side/bottom
// panels; hovering one shows a floating callout with its label and shortcut.
// The strip lives inside the existing status line (no extra row) so the
// viewHeight()/row arithmetic used by every overlay stays untouched.

type statusAction int

const (
	actNone statusAction = iota
	actTree
	actGit
	actChat
	actDebug
	actTerm
	actSplitV
	actSplitH
)

type statusIcon struct {
	act   statusAction
	glyph string
	tip   string // i18n key for the hover callout
}

var statusIconDefs = []statusIcon{
	{actTree, "▤", "status.tip_tree"},
	{actGit, "⎇", "status.tip_git"},
	{actChat, "✦", "status.tip_chat"},
	{actDebug, "◉", "status.tip_debug"},
	{actTerm, "❯", "status.tip_term"},
}

// statusIconsVisible reports whether the bottom line is the status bar or a
// git status/log line. Input prompts, review modes and full-screen overlays
// replace the bottom line, so the strip must not be rendered or hit-tested
// there. Keep this in sync with the bottom-line selection in View().
func (m Model) statusIconsVisible() bool {
	switch {
	case m.promptOpen, m.promptSave, m.quitConfirm,
		m.searchOpen, m.gotoOpen, m.conflictOpen, m.diffViewOpen,
		m.finderOpen, m.paletteOpen, m.langChooserOpen, m.pluginStoreOpen,
		m.aiInlineOpen, m.aiInlineBusy, m.aiFixOpen, m.aiFixBusy,
		m.aiReviewMode, m.aiFixReviewMode, m.agentReviewMode, m.chatReviewMode,
		m.agentPrompt, m.treeConfirm != "", m.aiCfgOpen, m.helpOpen:
		return false
	case m.gitOpen && (m.gitMode == gitModeStatus || m.gitMode == gitModeLog) && len(m.diffRows) > 0:
		// Inline git diff preview takes over the bottom line.
		return false
	case m.gitOpen && (m.gitMode == gitModeCommit || m.gitMode == gitModeBranch):
		return false
	}
	return true
}

// statusIconActive reports whether the panel behind an icon is currently open.
func (m Model) statusIconActive(a statusAction) bool {
	switch a {
	case actTree:
		return m.treeVisible
	case actGit:
		return m.gitOpen
	case actChat:
		return m.chatOpen
	case actDebug:
		return m.dapOpen
	case actTerm:
		return m.termOpen
	case actSplitV:
		return m.layout == splitVert
	case actSplitH:
		return m.layout == splitHoriz
	}
	return false
}

// statusIconsString renders the strip. Each cell is " glyph "; the active or
// hovered panel is highlighted.
func (m Model) statusIconsString() string {
	var b strings.Builder
	for _, d := range statusIconDefs {
		st := statusStyle
		if m.statusIconActive(d.act) || m.hoverIcon == d.act {
			st = statusHiStyle
		}
		b.WriteString(st.Render(" " + m.g.icon(d.act) + " "))
	}
	return b.String()
}

// statusIconAt returns the action whose painted cell contains column x, or
// actNone. Widths are measured with lipgloss.Width so multi-character glyphs
// (">_") stay aligned.
func (m Model) statusIconAt(x int) statusAction {
	pos := 0
	for _, d := range statusIconDefs {
		w := lipgloss.Width(" " + m.g.icon(d.act) + " ")
		if x >= pos && x < pos+w {
			return d.act
		}
		pos += w
	}
	return actNone
}

// statusIconX returns the starting column of an icon's cell, or -1.
func (m Model) statusIconX(a statusAction) int {
	pos := 0
	for _, d := range statusIconDefs {
		w := lipgloss.Width(" " + m.g.icon(d.act) + " ")
		if d.act == a {
			return pos
		}
		pos += w
	}
	return -1
}

// statusTip returns the hover label for an action.
func (m Model) statusTip(a statusAction) string {
	for _, d := range statusIconDefs {
		if d.act == a {
			return m.t(d.tip)
		}
	}
	switch a {
	case actSplitV:
		return m.t("status.tip_splitv")
	case actSplitH:
		return m.t("status.tip_splith")
	}
	return ""
}

// ---- Split icons (top-right corner of the tab bar) -------------------------

// splitIconGlyphs are the two right-aligned clickable cells at the end of the
// tab bar. ◫ reads as two side-by-side panes (vertical split), ▤ as stacked
// rows (horizontal split).
var splitIconGlyphs = []statusIcon{
	{actSplitV, "◫", "status.tip_splitv"},
	{actSplitH, "▤", "status.tip_splith"},
}

func (m Model) splitIconsWidth() int {
	w := 0
	for _, d := range splitIconGlyphs {
		w += lipgloss.Width(" " + m.g.splitIcon(d.act) + " ")
	}
	return w
}

// splitIconsVisible reports whether the split icons fit next to the tabs on
// the top row. Kept in sync with tabBar so hit-testing never maps a cell the
// renderer did not draw.
func (m Model) splitIconsVisible() bool {
	var lineW int
	for i := range m.tabs {
		lineW += lipgloss.Width(m.tabLabel(i))
	}
	return m.width-lineW >= m.splitIconsWidth()
}

func (m Model) splitIconsString() string {
	if !m.splitIconsVisible() {
		return ""
	}
	var b strings.Builder
	for _, d := range splitIconGlyphs {
		st := statusStyle
		if m.statusIconActive(d.act) || m.hoverSplit == d.act {
			st = statusHiStyle
		}
		b.WriteString(st.Render(" " + m.g.splitIcon(d.act) + " "))
	}
	return b.String()
}

// splitIconAt returns the split action whose painted cell contains column x
// on the tab bar row, or actNone.
func (m Model) splitIconAt(x int) statusAction {
	if !m.splitIconsVisible() {
		return actNone
	}
	start := m.width - m.splitIconsWidth()
	pos := start
	for _, d := range splitIconGlyphs {
		w := lipgloss.Width(" " + m.g.splitIcon(d.act) + " ")
		if x >= pos && x < pos+w {
			return d.act
		}
		pos += w
	}
	return actNone
}

// activateSplitIcon toggles the split the same way the keyboard does.
func (m *Model) activateSplitIcon(a statusAction) tea.Cmd {
	switch a {
	case actSplitV:
		m.toggleSplitVert()
	case actSplitH:
		m.toggleSplitHoriz()
	}
	return nil
}

// updateSplitHover tracks which top-right split icon the cursor is over.
func (m *Model) updateSplitHover(msg tea.MouseMotionMsg) {
	if msg.Y == 0 {
		m.hoverSplit = m.splitIconAt(msg.X)
		return
	}
	m.hoverSplit = actNone
}

// overlaySplitTooltip draws a one-row floating callout just under the tab bar,
// anchored to the hovered split icon (mirroring overlayStatusTooltip).
func (m Model) overlaySplitTooltip(rows []string) []string {
	if m.hoverSplit == actNone || !m.splitIconsVisible() {
		return rows
	}
	h := m.viewHeight()
	if h < 1 || h+1 >= len(rows) {
		return rows
	}
	tip := m.statusTip(m.hoverSplit)
	if tip == "" {
		return rows
	}
	text := statusHiStyle.Render(" " + tip + " ")
	w := lipgloss.Width(text)
	start := m.width - m.splitIconsWidth()
	x := start + 1 // center the callout under the hovered cell
	if m.hoverSplit == actSplitH {
		x = start + 3 + 1
	}
	if x+w > m.width {
		x = m.width - w
	}
	if x < 0 {
		x = 0
	}
	fill := m.width - x - w
	if fill < 0 {
		fill = 0
	}
	rows[1] = strings.Repeat(" ", x) + text + strings.Repeat(" ", fill)
	return rows
}

// activateStatusIcon performs the same action as the panel's keyboard
// shortcut, so mouse and keys stay in lockstep.
func (m *Model) activateStatusIcon(a statusAction) tea.Cmd {
	switch a {
	case actTree:
		m.toggleTree()
		return nil
	case actGit:
		if m.gitOpen {
			m.gitOpen = false
			m.gitFocus = false
			m.msg = ""
		} else {
			m.openGitPanel()
		}
		return nil
	case actChat:
		return m.toggleChat()
	case actDebug:
		return m.toggleDebugPanel()
	case actTerm:
		return m.toggleTerminal()
	}
	return nil
}

// paneStatusBar renders the status line docked at the bottom of each pane in a
// split. It reports that pane's own file, cursor position and file format. The
// app-wide line (icon strip, branch, transient messages, hints) stays on the
// single bottom row, so per-pane bars carry no interactive state of their own.
func (m Model) paneStatusBar(paneIdx int) string {
	p := &m.panes[paneIdx]
	t := &m.tabs[p.tabIdx]
	active := paneIdx == m.activePane

	mark := fmt.Sprintf("[%d] %s", paneIdx+1, t.name(m.baseDir()))
	lncol := m.t("status.lncol", t.buf.CurLine()+1, t.buf.Col()+1)
	fileInfo := ""
	langTag := ""
	if t.path != "" {
		endings := map[string]string{"lf": "LF", "crlf": "CRLF"}
		fileInfo = " " + endings[t.lineEnding] + "/" + strings.ToUpper(t.encoding)
		if lang := syntax.Lang(t.path); lang != "" {
			langTag = " " + lang
		}
	}
	branchSuffix := ""
	if active && m.repo != nil {
		if b := m.repo.Branch(); b != "" {
			branchSuffix = " (" + b + ")"
		}
	}
	rightBar := langStyle.Render(langTag) + statusStyle.Render(fileInfo) + hintStyle.Render(branchSuffix+" "+lncol)

	left := statusStyle.Render(mark)
	if active {
		left = statusHiStyle.Render(mark)
	}

	fill := m.paneTotalWidth(paneIdx) - lipgloss.Width(left) - lipgloss.Width(rightBar)
	if fill < 0 {
		fill = 0
	}
	return left + statusStyle.Render(strings.Repeat(" ", fill)) + rightBar
}

// overlayStatusTooltip draws a one-row floating callout directly above the
// status bar, anchored to the hovered icon. It replaces that editor row for
// the duration of the hover, mirroring how overlayCompletion splices its
// popup over full rows (spaces + content + fill, no reflow).
func (m Model) overlayStatusTooltip(rows []string) []string {
	if m.hoverIcon == actNone || !m.statusIconsVisible() {
		return rows
	}
	row := m.statusBarRow() - 1 // the row directly above the status bar
	if row < 1 || row >= len(rows) {
		return rows
	}
	tip := m.statusTip(m.hoverIcon)
	if tip == "" {
		return rows
	}
	text := statusHiStyle.Render(" " + tip + " ")
	w := lipgloss.Width(text)
	x := m.statusIconX(m.hoverIcon)
	if x < 0 {
		return rows
	}
	if x+w > m.width {
		x = m.width - w
	}
	if x < 0 {
		x = 0
	}
	fill := m.width - x - w
	if fill < 0 {
		fill = 0
	}
	rows[row] = strings.Repeat(" ", x) + text + strings.Repeat(" ", fill)
	return rows
}
