package editor

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"dmed/internal/i18n"
)

// Mouse handling across every pane: the tab bar, left rail (agent/git/tree),
// bottom overlays (finder, palette, language chooser, plugin store, completion,
// terminal), the right AI chat rail, diff views, and the buffer itself.
// Mouse coordinates are zero-based as delivered by bubbletea.

// tabAtX returns the tab whose painted label contains column x, or -1.
func (m Model) tabAtX(x int) int {
	pos := 0
	for i := range m.tabs {
		w := lipgloss.Width(m.tabLabel(i))
		if x >= pos && x < pos+w {
			return i
		}
		pos += w
	}
	return -1
}

// The bottom overlay panels are appended after the status bar, one after the
// other, in the same order they are rendered.
func (m Model) finderStartRow() int { return m.viewHeight() + 2 }
func (m Model) paletteStartRow() int {
	return m.finderStartRow() + m.finderExtraRows()
}
func (m Model) langChooserStartRow() int {
	return m.paletteStartRow() + m.paletteExtraRows()
}
func (m Model) storeStartRow() int {
	return m.langChooserStartRow() + m.langChooserExtraRows()
}

// complStartRow is the screen row the completion popup occupies: it floats
// right below the edit line under the cursor.
func (m Model) complStartRow() int {
	_, sy := m.cursorScreenPos()
	return sy + 1
}
func (m Model) termStartRow() int { return m.storeStartRow() + m.pluginStoreExtraRows() }

func (m *Model) handleMouseClick(msg tea.MouseClickMsg) tea.Cmd {
	x, y := msg.X, msg.Y
	h := m.viewHeight()

	// The tab bar is always row 0.
	if y == 0 {
		if idx := m.tabAtX(x); idx >= 0 {
			m.setActiveTab(idx)
		}
		return nil
	}

	// Panels stacked above the editor.
	if handled, cmd := m.clickOverlay(y); handled {
		return cmd
	}

	// Right AI chat rail.
	if m.chatOpen && x >= m.width-m.rightRailWidth() && y >= 1 && y <= h {
		m.chatFocus = true
		m.gitFocus = false
		return nil
	}

	// Editor area rows.
	if y < 1 || y > h {
		return nil
	}

	// Left sidebar rail: agent tasks, git panel, project tree.
	if x < m.leftRailWidth() {
		return m.clickLeftRail(x, y)
	}

	// Git inline diff preview fills the rest of the editor area.
	if m.gitOpen && (m.gitMode == gitModeStatus || m.gitMode == gitModeLog) && len(m.diffRows) > 0 {
		m.gitDiffFocused = true
		return nil
	}

	// Full-screen modes replace the buffer; their content is scrolled with
	// the wheel. A click places the cursor only in the buffer.
	if m.diffViewOpen || m.aiReviewMode || m.agentReviewMode || m.conflictOpen || m.aiCfgOpen || m.helpOpen {
		return nil
	}

	return m.clickBuffer(x, y, msg.Mod)
}

// clickOverlay routes clicks on the stacked bottom panels. It reports whether
// the click was consumed, and may return a command (palette / store actions).
func (m *Model) clickOverlay(y int) (bool, tea.Cmd) {
	f := m.viewHeight() + 2

	if m.finderOpen {
		n := len(m.finderHits)
		if y >= f && y <= f+n {
			if y < f+n {
				m.finderSel = y - f
				path := m.finderHits[m.finderSel]
				m.finderOpen = false
				m.focusOrOpen(path)
			}
			return true, nil
		}
		f += m.finderExtraRows()
	}

	if m.paletteOpen {
		hits := m.filterPalette()
		n := len(hits)
		if n > 8 {
			n = 8
		}
		if y >= f && y <= f+n {
			if y < f+n {
				global := m.paletteOffset + (y - f)
				if global >= 0 && global < len(hits) {
					m.paletteSel = global
					m.clampPalette(hits)
					sel := hits[m.paletteSel]
					m.paletteOpen = false
					m.paletteSel = 0
					m.paletteOffset = 0
					return true, sel.action(m)
				}
			}
			return true, nil
		}
		f += m.paletteExtraRows()
	}

	if m.langChooserOpen {
		langs := i18n.Supported()
		if y >= f+1 && y <= f+len(langs) {
			if y < f+len(langs) {
				sel := y - (f + 1)
				m.langChooserSel = sel
				m.langChooserOpen = false
				m.setLang(langs[sel].Code)
				return true, nil
			}
		}
		f += m.langChooserExtraRows()
	}

	if m.pluginStoreOpen {
		if y >= f+1 && y <= f+len(m.storeItems) {
			sel := y - (f + 1)
			if sel >= 0 && sel < len(m.storeItems) {
				m.pluginStoreSel = sel
				m.pluginStoreOpen = false
				return true, m.activateStoreItem(m.storeItems[sel])
			}
			return true, nil
		}
		f += m.pluginStoreExtraRows()
	}

	// The completion popup is a floating window anchored under the cursor, not
	// part of the bottom stack.
	if m.complOpen {
		vis := len(m.complItems)
		if vis > complVisible {
			vis = complVisible
		}
		start := m.complStartRow()
		if y == start && vis > 0 {
			return true, nil // title row
		}
		if y > start && y < start+1+vis {
			global := m.complOffset + (y - (start + 1))
			if global >= 0 && global < len(m.complItems) {
				m.complSel = global
				m.clampCompletion()
				m.acceptCompletion()
			}
			return true, nil
		}
	}

	if m.termOpen {
		if y >= f && y < f+m.termPanelHeight() {
			return true, nil
		}
	}

	return false, nil
}

// clickLeftRail selects an item under the pointer in the agent task list, git
// panel, or project tree.
func (m *Model) clickLeftRail(x, y int) tea.Cmd {
	h := m.viewHeight()
	switch {
	case m.agentOpen:
		idx := m.agentOffset + (y - 1)
		if tasks := m.agentQueue.Snapshot(); idx >= 0 && idx < len(tasks) {
			m.agentSel = idx
		}
		m.agentFocus = true
		m.gitFocus = false
		m.chatFocus = false
	case m.gitOpen:
		m.gitFocus = true
		m.chatFocus = false
		switch m.gitMode {
		case gitModeStatus:
			idx := m.gitOffset + (y - 1)
			if idx >= 0 && idx < len(m.gitFiles) {
				m.gitSel = idx
				m.clampGitScroll()
				m.refreshGitDiffPreview()
			}
			m.gitDiffFocused = false
		case gitModeLog:
			idx := m.gitLogOffset + (y-1)/2
			if idx >= 0 && idx < len(m.gitLogEntries) {
				m.gitLogSel = idx
				m.clampGitLogScroll()
				m.showLogDiff()
			}
			m.gitDiffFocused = false
		case gitModeBranch:
			idx := m.gitBranchOffset + (y - 1)
			if idx >= 0 && idx < len(m.gitBranchList) {
				m.gitBranchSel = idx
				m.clampGitBranchScroll()
			}
		}
	case m.sidebarOn():
		idx := m.treeOffset + (y - 1)
		if idx >= 0 && idx < len(m.treeRows) {
			m.treeSel = idx
			m.treeFocus = true
			m.gitFocus = false
			m.chatFocus = false
			m.clampTreeScroll(h)
		}
	}
	return nil
}

// clickBuffer places the cursor (or a secondary cursor with Alt+Click) in the
// pane under the pointer and engages drag-selection.
func (m *Model) clickBuffer(x, y int, mod tea.KeyMod) tea.Cmd {
	leftW := m.leftRailWidth()
	editorRow := y - 1

	// Pick the pane that was actually clicked in a split layout.
	if m.layout == splitVert {
		w0 := m.paneTotalWidth(0)
		if x >= leftW+w0+1 {
			m.activePane = 1
		} else {
			m.activePane = 0
		}
	} else if m.layout == splitHoriz {
		if editorRow >= m.paneViewHeight(0)+1 {
			m.activePane = 1
		} else {
			m.activePane = 0
		}
	}

	p := m.curPane()
	t := &m.tabs[p.tabIdx]
	ln := editorRow + p.offsetY
	if ln >= t.buf.LineCount() {
		ln = t.buf.LineCount() - 1
	}
	if ln < 0 {
		ln = 0
	}

	gw := m.gutterWidthForTab(t)
	clickX := x - leftW - gw + p.offsetX
	if clickX < 0 {
		clickX = 0
	}

	rawCol := expandedToRawCol(t.buf.LineAt(ln), clickX, m.cfg.Editor.TabWidth)
	if lineLen := t.buf.LineLen(ln); rawCol > lineLen {
		rawCol = lineLen
	}

	if mod&tea.ModAlt != 0 {
		if t.buf.AddCursor(ln, rawCol, rawCol, rawCol) {
			m.msg = m.t("msg.added_cursor")
		}
		return nil
	}

	t.buf.SetCursor(ln, rawCol)
	t.buf.Deselect()
	m.treeFocus = false
	m.agentFocus = false
	m.gitFocus = false
	m.gitDiffFocused = false
	m.chatFocus = false
	m.mouseDown = true
	return nil
}

func (m *Model) handleMouseWheel(msg tea.MouseWheelMsg) tea.Cmd {
	x, y := msg.X, msg.Y
	h := m.viewHeight()
	dir := 0
	switch msg.Button {
	case tea.MouseWheelUp:
		dir = -1
	case tea.MouseWheelDown:
		dir = 1
	default:
		return nil
	}

	// While the diff has focus, the wheel always scrolls it.
	if m.gitDiffFocused {
		m.diffOffsetY += dir
		m.clampDiffScroll(h)
		return nil
	}

	// Terminal panel.
	if m.termOpen && y >= m.termStartRow() && y < m.termStartRow()+m.termPanelHeight() {
		step := m.termPanelHeight() / 2
		if step < 1 {
			step = 1
		}
		m.termScroll += dir * step
		if maxBack := len(m.termLines) - 1; m.termScroll > maxBack {
			m.termScroll = maxInt(0, maxBack)
		}
		if m.termScroll < 0 {
			m.termScroll = 0
		}
		return nil
	}

	// Completion popup: move the selection instead of the buffer.
	if m.complOpen {
		start := m.complStartRow()
		if y > start && y <= start+m.complExtraRows()-1 {
			if n := len(m.complItems); n > 0 {
				m.complSel += dir
				if m.complSel < 0 {
					m.complSel = 0
				}
				if m.complSel > n-1 {
					m.complSel = n - 1
				}
				m.clampCompletion()
			}
			return nil
		}
	}

	// Right AI chat rail.
	if m.chatOpen && x >= m.width-m.rightRailWidth() && y >= 1 && y <= h {
		step := m.paneViewHeight(m.activePane) / 2
		if step < 1 {
			step = 1
		}
		bodyH := h - 2
		if bodyH < 1 {
			bodyH = 1
		}
		m.chatScroll += dir * step
		if maxBack := len(m.chatRows) - bodyH; m.chatScroll > maxBack {
			m.chatScroll = maxInt(0, maxBack)
		}
		if m.chatScroll < 0 {
			m.chatScroll = 0
		}
		return nil
	}

	if y < 1 || y > h {
		return nil
	}

	// Full-width side-by-side views.
	switch {
	case m.diffViewOpen:
		m.diffOffsetY += dir
		m.clampDiffScroll(h)
		return nil
	case m.aiReviewMode:
		m.aiReviewOffY += dir
		if m.aiReviewOffY < 0 {
			m.aiReviewOffY = 0
		}
		if maxOff := len(m.aiReviewRows) - 1; m.aiReviewOffY > maxOff {
			m.aiReviewOffY = maxInt(0, maxOff)
		}
		return nil
	case m.agentReviewMode:
		m.agentReviewOffY += dir
		if m.agentReviewOffY < 0 {
			m.agentReviewOffY = 0
		}
		if maxOff := len(m.agentReviewRows) - 1; m.agentReviewOffY > maxOff {
			m.agentReviewOffY = maxInt(0, maxOff)
		}
		return nil
	case m.conflictOpen && len(m.conflictRows) > 0:
		m.conflictOffY += dir
		if m.conflictOffY < 0 {
			m.conflictOffY = 0
		}
		if maxOff := len(m.conflictRows) - 1; m.conflictOffY > maxOff {
			m.conflictOffY = maxInt(0, maxOff)
		}
		return nil
	case m.aiCfgOpen, m.helpOpen:
		return nil
	}

	// Git inline diff preview.
	if m.gitOpen && (m.gitMode == gitModeStatus || m.gitMode == gitModeLog) && len(m.diffRows) > 0 {
		if x >= m.leftRailWidth() {
			m.gitDiffFocused = true
			m.diffOffsetY += dir
			m.clampDiffScroll(h)
			return nil
		}
	}

	// Left rail lists.
	if x < m.leftRailWidth() {
		return m.wheelLeftRail(dir)
	}

	// Buffer scroll.
	p := m.curPane()
	if dir < 0 {
		if p.offsetY > 0 {
			p.offsetY--
		}
	} else {
		t := &m.tabs[p.tabIdx]
		maxOff := t.buf.LineCount() - m.paneViewHeight(m.activePane)
		if maxOff < 0 {
			maxOff = 0
		}
		if p.offsetY < maxOff {
			p.offsetY++
		}
	}
	return nil
}

// wheelLeftRail moves the selection within the left-rail lists, keeping the
// selection on screen through the existing clamp helpers.
func (m *Model) wheelLeftRail(dir int) tea.Cmd {
	switch {
	case m.agentOpen:
		if tasks := m.agentQueue.Snapshot(); len(tasks) > 0 {
			m.agentSel += dir
			if m.agentSel < 0 {
				m.agentSel = 0
			}
			if m.agentSel > len(tasks)-1 {
				m.agentSel = len(tasks) - 1
			}
		}
	case m.gitOpen:
		m.gitFocus = true
		m.chatFocus = false
		switch m.gitMode {
		case gitModeStatus:
			if len(m.gitFiles) > 0 {
				m.gitSel += dir
				if m.gitSel < 0 {
					m.gitSel = 0
				}
				if m.gitSel > len(m.gitFiles)-1 {
					m.gitSel = len(m.gitFiles) - 1
				}
				m.clampGitScroll()
				m.refreshGitDiffPreview()
			}
		case gitModeLog:
			if len(m.gitLogEntries) > 0 {
				m.gitLogSel += dir
				if m.gitLogSel < 0 {
					m.gitLogSel = 0
				}
				if m.gitLogSel > len(m.gitLogEntries)-1 {
					m.gitLogSel = len(m.gitLogEntries) - 1
				}
				m.clampGitLogScroll()
				m.showLogDiff()
			}
		case gitModeBranch:
			if len(m.gitBranchList) > 0 {
				m.gitBranchSel += dir
				if m.gitBranchSel < 0 {
					m.gitBranchSel = 0
				}
				if m.gitBranchSel > len(m.gitBranchList)-1 {
					m.gitBranchSel = len(m.gitBranchList) - 1
				}
				m.clampGitBranchScroll()
			}
		}
	case m.sidebarOn():
		if len(m.treeRows) > 0 {
			m.treeSel += dir
			if m.treeSel < 0 {
				m.treeSel = 0
			}
			if m.treeSel > len(m.treeRows)-1 {
				m.treeSel = len(m.treeRows) - 1
			}
			m.treeFocus = true
			m.gitFocus = false
			m.chatFocus = false
			m.clampTreeScroll(m.viewHeight())
		}
	}
	return nil
}
