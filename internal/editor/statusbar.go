package editor

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
		b.WriteString(st.Render(" " + d.glyph + " "))
	}
	return b.String()
}

// statusIconAt returns the action whose painted cell contains column x, or
// actNone. Widths are measured with lipgloss.Width so multi-character glyphs
// (">_") stay aligned.
func (m Model) statusIconAt(x int) statusAction {
	pos := 0
	for _, d := range statusIconDefs {
		w := lipgloss.Width(" " + d.glyph + " ")
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
		w := lipgloss.Width(" " + d.glyph + " ")
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
	return ""
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

// overlayStatusTooltip draws a one-row floating callout directly above the
// status bar, anchored to the hovered icon. It replaces that editor row for
// the duration of the hover, mirroring how overlayCompletion splices its
// popup over full rows (spaces + content + fill, no reflow).
func (m Model) overlayStatusTooltip(rows []string) []string {
	if m.hoverIcon == actNone || !m.statusIconsVisible() {
		return rows
	}
	h := m.viewHeight()
	if h < 1 || h >= len(rows) {
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
	rows[h] = strings.Repeat(" ", x) + text + strings.Repeat(" ", fill)
	return rows
}
