package editor

import (
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/vcs"
)

// The side-by-side diff view (opened from the Git panel with 'd') shows the
// HEAD version of a file on the left and the current text on the right.
// It is read-only and renders on top of the regular editor area.

func splitLines(s string) []string {
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" && strings.HasSuffix(s, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func (m *Model) tabByPath(path string) *tab {
	for i := range m.tabs {
		if m.tabs[i].path == path || filepath.Clean(m.tabs[i].path) == filepath.Clean(path) {
			return &m.tabs[i]
		}
	}
	return nil
}

func (m *Model) openDiffView() {
	if m.gitSel >= len(m.gitFiles) {
		m.msg = "no file selected"
		return
	}
	fs := m.gitFiles[m.gitSel]
	r := m.repoForCur()
	if r == nil {
		m.msg = "no git repo"
		return
	}
	abs := filepath.Join(r.Root, filepath.FromSlash(fs.Path))

	headText, err := r.HeadContent(abs)
	if err != nil {
		headText = "" // untracked or new file: nothing on the left side
	}

	right := ""
	if t := m.tabByPath(abs); t != nil {
		right = t.buf.Text()
	} else if data, rerr := os.ReadFile(abs); rerr == nil {
		right = strings.ReplaceAll(string(data), "\r\n", "\n")
	} else {
		m.msg = "cannot read: " + fs.Path
		return
	}

	m.diffPath = fs.Path
	m.diffHeadLines = splitLines(headText)
	m.diffRightLines = splitLines(right)
	m.diffRows = vcs.SideBySide(headText, right)
	m.diffOffsetY = 0
	m.diffOffsetX = 0
	m.diffViewOpen = true
	m.msg = "diff: " + fs.Path
}

func (m *Model) closeDiffView() {
	m.diffViewOpen = false
	m.msg = ""
}

func (m *Model) clampDiffScroll(h int) {
	maxY := len(m.diffRows) - h
	if maxY < 0 {
		maxY = 0
	}
	if m.diffOffsetY > maxY {
		m.diffOffsetY = maxY
	}
	if m.diffOffsetY < 0 {
		m.diffOffsetY = 0
	}
	if m.diffOffsetX < 0 {
		m.diffOffsetX = 0
	}
}

func (m *Model) handleDiffView(msg tea.KeyPressMsg) tea.Cmd {
	h := m.viewHeight()
	switch msg.String() {
	case "esc", "q", "d":
		m.closeDiffView()
	case "up", "k":
		m.diffOffsetY--
	case "down", "j":
		m.diffOffsetY++
	case "pgup":
		m.diffOffsetY -= h
	case "pgdn":
		m.diffOffsetY += h
	case "home", "g":
		m.diffOffsetY = 0
	case "end", "G":
		m.diffOffsetY = len(m.diffRows)
	case "left", "h":
		m.diffOffsetX -= 8
	case "right", "l":
		m.diffOffsetX += 8
	default:
		return nil
	}
	m.clampDiffScroll(h)
	return nil
}
