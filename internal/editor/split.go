package editor

import tea "github.com/charmbracelet/bubbletea"

type pane struct {
	tabIdx  int
	offsetX int
	offsetY int
}

type splitLayout int

const (
	splitNone splitLayout = iota
	splitVert
	splitHoriz
)

func (m *Model) initPanes() {
	idx := len(m.tabs) - 1
	if idx < 0 {
		idx = 0
	}
	m.panes = []pane{{tabIdx: idx}}
	m.activePane = 0
	m.layout = splitNone
}

func (m Model) activeTabIndex() int { return m.panes[m.activePane].tabIdx }

func (m *Model) curPane() *pane { return &m.panes[m.activePane] }

func (m *Model) setActiveTab(idx int) {
	if idx >= 0 && idx < len(m.tabs) {
		m.panes[m.activePane].tabIdx = idx
	}
}

func (m Model) editorAreaWidth() int { return m.width - m.leftRailWidth() }

func (m Model) paneTotalWidth(paneIdx int) int {
	ew := m.editorAreaWidth()
	if m.layout == splitVert {
		sep := 1
		if paneIdx == 0 {
			return (ew - sep) / 2
		}
		return ew - sep - (ew-sep)/2
	}
	return ew
}

func (m Model) paneViewHeight(paneIdx int) int {
	h := m.viewHeight()
	if m.layout == splitHoriz {
		// Reserve 1 line for separator
		sepLine := 1
		if paneIdx == 0 {
			return (h - sepLine) / 2
		}
		return h - sepLine - (h-sepLine)/2
	}
	if h < 1 {
		h = 1
	}
	return h
}

func (m *Model) splitVert() {
	if m.layout != splitNone {
		return
	}
	// Find a different tab for the new pane
	newTabIdx := m.findOtherTabIdx()
	m.panes = append(m.panes, pane{tabIdx: newTabIdx})
	m.layout = splitVert
	m.activePane = 1
	m.msg = "split vertical"
}

func (m *Model) splitHoriz() {
	if m.layout != splitNone {
		return
	}
	// Find a different tab for the new pane
	newTabIdx := m.findOtherTabIdx()
	m.panes = append(m.panes, pane{tabIdx: newTabIdx})
	m.layout = splitHoriz
	m.activePane = 1
	m.msg = "split horizontal"
}

func (m *Model) findOtherTabIdx() int {
	cur := m.activeTabIndex()
	if len(m.tabs) == 1 {
		// Only one tab, both panes show it
		return 0
	}
	// Prefer next tab, wrap to previous if at end
	if cur < len(m.tabs)-1 {
		return cur + 1
	}
	return cur - 1
}

func (m *Model) focusOtherPane() {
	if m.layout == splitNone {
		return
	}
	m.activePane = 1 - m.activePane
	m.msg = ""
}

func (m *Model) closePane() tea.Cmd {
	if m.layout == splitNone {
		return nil
	}
	other := 1 - m.activePane
	m.panes = []pane{m.panes[other]}
	m.activePane = 0
	m.layout = splitNone
	m.msg = ""
	return nil
}

func (m *Model) fixPaneTabsAfterClose(closedIdx int) {
	for i := range m.panes {
		ti := m.panes[i].tabIdx
		switch {
		case ti == closedIdx:
			if closedIdx > 0 {
				m.panes[i].tabIdx = closedIdx - 1
			} else {
				m.panes[i].tabIdx = 0
			}
		case ti > closedIdx:
			m.panes[i].tabIdx--
		}
	}
}
