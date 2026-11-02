package editor

import tea "charm.land/bubbletea/v2"

// jumpPoint is one entry of the pane's jump history: a caret position. It is
// named by path rather than by tab index, because indices are renumbered by
// every close (fixPaneTabsAfterClose) while a path still resolves through
// focusOrOpen — even after the tab was closed and needs reopening.
type jumpPoint struct {
	path string
	line int
	col  int
}

type pane struct {
	tabIdx   int
	offsetX  int
	offsetY  int
	wordWrap bool
	backs    []jumpPoint
	forwards []jumpPoint

	// recent are the tab indices this pane moved off, most recent first: the raw
	// history the MRU chord cycles. Indices rather than paths, unlike jumpPoint —
	// a closed tab must drop out of the list instead of being reopened from disk,
	// and the untitled buffer has no path at all (two tabs may also share one).
	// recentWalk is the order frozen for the walk in progress and recentIdx the
	// position in it; the freeze is what makes the cycle walk instead of
	// ping-pong, because the live order always starts with the tab the pane shows
	// and the walk keeps changing that tab.
	recent     []int
	recentWalk []int
	recentIdx  int
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
	m.panes = []pane{{tabIdx: idx, wordWrap: m.cfg.Editor.WordWrap}}
	m.activePane = 0
	m.layout = splitNone
}

func (m Model) activeTabIndex() int { return m.panes[m.activePane].tabIdx }

func (m *Model) curPane() *pane { return &m.panes[m.activePane] }

func (m *Model) setActiveTab(idx int) {
	if idx < 0 || idx >= len(m.tabs) {
		return
	}
	c := m.curPane()
	if c.tabIdx != idx {
		c.recent = recordRecent(c.recent, c.tabIdx)
		c.recentWalk, c.recentIdx = nil, 0
	}
	c.tabIdx = idx
}

// setPaneTabQuiet is the MRU walk's own focus step: it moves the pane and leaves
// the recency history alone. A walk that recorded each tab it left would reorder
// the list under itself, and N presses would ping-pong between two tabs instead of
// reaching the N-th most recent one. Any other focus change goes through
// setActiveTab, which records and restarts the cycle.
func (m *Model) setPaneTabQuiet(idx int) {
	if idx < 0 || idx >= len(m.tabs) {
		return
	}
	m.curPane().tabIdx = idx
}

// recordRecent puts idx at the front of the recency list, dropping the older copy
// of it so a tab visited twice is not walked twice.
func recordRecent(recent []int, idx int) []int {
	out := make([]int, 0, len(recent)+1)
	out = append(out, idx)
	for _, v := range recent {
		if v != idx {
			out = append(out, v)
		}
	}
	return out
}

func (m Model) editorAreaWidth() int {
	return m.width - m.leftRailWidth() - m.rightRailWidth()
}

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

// paneContentHeight returns how many editor rows a pane actually draws. In a
// split every pane's cell reserves its bottom row for its own status bar, so
// the content region is one row shorter than the cell; the extra row exists
// only in split mode, where paneViewHeight still counts it for hit-testing
// and cell geometry.
func (m Model) paneContentHeight(paneIdx int) int {
	if m.layout == splitNone {
		return m.paneViewHeight(paneIdx)
	}
	return m.paneViewHeight(paneIdx) - 1
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

// toggleSplitVert opens a vertical split, or collapses the current split when
// one is already open.
func (m *Model) toggleSplitVert() {
	if m.layout != splitNone {
		m.closePane()
		return
	}
	m.splitVert()
}

// toggleSplitHoriz opens a horizontal split, or collapses the current split
// when one is already open.
func (m *Model) toggleSplitHoriz() {
	if m.layout != splitNone {
		m.closePane()
		return
	}
	m.splitHoriz()
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
		// The same renumbering for the recency list: the closed tab drops out and
		// everything above it moves down. The cycle restarts, because its position
		// counted tabs in the list that just moved.
		if len(m.panes[i].recent) > 0 {
			out := make([]int, 0, len(m.panes[i].recent))
			for _, v := range m.panes[i].recent {
				switch {
				case v == closedIdx:
				case v > closedIdx:
					out = append(out, v-1)
				default:
					out = append(out, v)
				}
			}
			m.panes[i].recent = out
		}
		m.panes[i].recentWalk, m.panes[i].recentIdx = nil, 0
	}
}
