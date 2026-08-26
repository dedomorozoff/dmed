package editor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"dmed/internal/syntax"
	"dmed/internal/vcs"
)

// gitPanelMode controls what the Git panel is showing.
type gitPanelMode int

const (
	gitModeStatus gitPanelMode = iota // file list
	gitModeCommit                     // commit message input
	gitModeLog                        // commit history list
)

func (m *Model) openGitPanel() {
	m.gitOpen = true
	m.gitMode = gitModeStatus
	m.gitCommitIn = nil
	m.gitDiffFocused = false
	m.refreshGitFiles()
	m.refreshGitDiffPreview()
}

func (m *Model) refreshGitFiles() {
	r := m.repoForCur()
	if r == nil {
		m.gitFiles = nil
		return
	}
	files, err := r.StatusFiles()
	if err != nil {
		m.gitFiles = nil
		m.msg = "git status error: " + err.Error()
		return
	}
	m.gitFiles = files
	if m.gitSel >= len(m.gitFiles) {
		m.gitSel = maxInt(0, len(m.gitFiles)-1)
	}
	m.clampGitScroll()
}

func (m *Model) repoForCur() *vcs.Repo {
	t := m.cur()
	r := m.repo
	if r == nil || (t.path != "" && !strings.HasPrefix(t.path, r.Root)) {
		if t.path != "" {
			if found, err := vcs.Open(filepath.Dir(t.path)); err == nil {
				return found
			}
		}
		return m.repo
	}
	return r
}

func (m Model) gitListHeight() int {
	n := len(m.gitFiles)
	if room := m.viewHeight(); n > room {
		n = room
	}
	return n
}

func (m *Model) clampGitScroll() {
	w := m.gitListHeight()
	if w <= 0 {
		m.gitOffset = 0
		return
	}
	if m.gitSel < m.gitOffset {
		m.gitOffset = m.gitSel
	}
	if m.gitSel >= m.gitOffset+w {
		m.gitOffset = m.gitSel - w + 1
	}
	if off := len(m.gitFiles) - w; m.gitOffset > off {
		m.gitOffset = off
	}
	if m.gitOffset < 0 {
		m.gitOffset = 0
	}
}

// refreshGitDiffPreview computes the side-by-side diff for the currently
// selected file in the git panel so it can be shown inline.
func (m *Model) refreshGitDiffPreview() {
	if m.gitSel >= len(m.gitFiles) {
		m.diffRows = nil
		m.diffHeadLines = nil
		m.diffRightLines = nil
		m.diffPath = ""
		return
	}
	fs := m.gitFiles[m.gitSel]
	r := m.repoForCur()
	if r == nil {
		m.diffRows = nil
		return
	}
	abs := filepath.Join(r.Root, filepath.FromSlash(fs.Path))

	headText, err := r.HeadContent(abs)
	if err != nil {
		headText = "" // untracked
	}

	right := ""
	if t := m.tabByPath(abs); t != nil {
		right = t.buf.Text()
	} else if data, rerr := os.ReadFile(abs); rerr == nil {
		right = strings.ReplaceAll(string(data), "\r\n", "\n")
	}

	m.diffPath = fs.Path
	m.diffHeadLines = splitLines(headText)
	m.diffRightLines = splitLines(right)
	m.diffRows = vcs.SideBySide(headText, right)
	m.diffOffsetY = 0
	m.diffOffsetX = 0
	m.diffHeadSyntax = syntax.Default().HighlightBuffer(fs.Path, headText)
	m.diffRightSyntax = syntax.Default().HighlightBuffer(fs.Path, right)
}

func (m *Model) showTree() {
	m.treeVisible = true
	m.treeFocus = true
	m.rebuildTree()
}

// --- Git log ---

func (m *Model) refreshGitLog() {
	r := m.repoForCur()
	if r == nil {
		m.gitLogEntries = nil
		return
	}
	entries, err := r.Log(50)
	if err != nil {
		m.gitLogEntries = nil
		m.msg = "git log error: " + err.Error()
		return
	}
	m.gitLogEntries = entries
	if m.gitLogSel >= len(m.gitLogEntries) {
		m.gitLogSel = maxInt(0, len(m.gitLogEntries)-1)
	}
	m.clampGitLogScroll()
}

func (m Model) gitLogListHeight() int {
	// Each log entry takes 2 rows (hash+subject + author+time)
	n := len(m.gitLogEntries) * 2
	if room := m.viewHeight(); n > room {
		n = room
	}
	return n
}

func (m *Model) clampGitLogScroll() {
	maxRows := m.gitLogListHeight()
	if maxRows <= 0 {
		m.gitLogOffset = 0
		return
	}
	// Each entry is 2 rows; selected entry's top row = sel*2
	selRow := m.gitLogSel * 2
	if selRow < m.gitLogOffset {
		m.gitLogOffset = selRow
	}
	if selRow+1 >= m.gitLogOffset+maxRows {
		m.gitLogOffset = selRow + 2 - maxRows
	}
	totalRows := len(m.gitLogEntries) * 2
	maxOff := totalRows - maxRows
	if maxOff < 0 {
		maxOff = 0
	}
	if m.gitLogOffset > maxOff {
		m.gitLogOffset = maxOff
	}
	if m.gitLogOffset < 0 {
		m.gitLogOffset = 0
	}
}

func (m *Model) openLogView() {
	m.gitMode = gitModeLog
	m.gitLogSel = 0
	m.gitLogOffset = 0
	m.gitDiffFocused = false
	m.refreshGitLog()
	m.showLogDiff()
}

func (m *Model) showLogDiff() {
	if m.gitLogSel >= len(m.gitLogEntries) {
		m.diffRows = nil
		return
	}
	r := m.repoForCur()
	if r == nil {
		m.diffRows = nil
		return
	}
	entry := m.gitLogEntries[m.gitLogSel]
	diffs, err := r.CommitDiff(entry.FullHash)
	if err != nil {
		m.diffRows = nil
		m.msg = "diff error: " + err.Error()
		return
	}
	// Build concatenated diff with file separator lines.
	var headAll, rightAll []string
	var rowsAll []vcs.DiffRow
	for fi, d := range diffs {
		if fi > 0 {
			// Empty separator line between files
			headAll = append(headAll, "")
			rightAll = append(rightAll, "")
			rowsAll = append(rowsAll, vcs.DiffRow{Left: len(headAll) - 1, Right: len(rightAll) - 1, Type: vcs.DiffNone})
		}
		// File header line
		header := "── " + d.Path + " ──"
		headAll = append(headAll, header)
		rightAll = append(rightAll, "")
		rowsAll = append(rowsAll, vcs.DiffRow{Left: len(headAll) - 1, Right: -1, Type: vcs.DiffNone})

		offsetL := len(headAll)
		offsetR := len(rightAll)
		for _, dr := range d.DiffRows {
			nr := dr
			if dr.Left >= 0 {
				nr.Left = dr.Left + offsetL
			}
			if dr.Right >= 0 {
				nr.Right = dr.Right + offsetR
			}
			rowsAll = append(rowsAll, nr)
		}
		headAll = append(headAll, d.HeadLines...)
		rightAll = append(rightAll, d.RightLines...)
	}
	m.diffPath = entry.Hash + " " + entry.Subject
	m.diffHeadLines = headAll
	m.diffRightLines = rightAll
	m.diffRows = rowsAll
	m.diffOffsetY = 0
	m.diffOffsetX = 0
}

// --- Dispatch ---

func (m *Model) handleGit(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+b", "f9":
		m.gitOpen = false
		m.msg = ""
		m.showTree()
		return nil
	}
	switch m.gitMode {
	case gitModeStatus:
		return m.handleGitStatus(msg)
	case gitModeCommit:
		return m.handleGitCommit(msg)
	case gitModeLog:
		return m.handleGitLog(msg)
	}
	return nil
}

// --- Status mode ---

func (m *Model) handleGitStatus(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()

	// Global keys
	switch key {
	case "esc", "ctrl+g":
		m.gitOpen = false
		m.gitDiffFocused = false
		m.msg = ""
		return nil
	case "q":
		if m.gitDiffFocused {
			m.gitDiffFocused = false
			return nil
		}
		m.gitOpen = false
		m.gitDiffFocused = false
		m.msg = ""
		return nil
	}

	// Diff area has focus — scroll
	if m.gitDiffFocused {
		h := m.viewHeight()
		switch key {
		case "left", "tab":
			m.gitDiffFocused = false
			return nil
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
		case "h":
			m.diffOffsetX -= 8
		case "right", "l":
			m.diffOffsetX += 8
		}
		m.clampDiffScroll(h)
		return nil
	}

	// File list has focus
	switch key {
	case "right", "tab":
		if len(m.diffRows) > 0 {
			m.gitDiffFocused = true
		}
		return nil
	case "up", "k":
		if m.gitSel > 0 {
			m.gitSel--
			m.clampGitScroll()
			m.refreshGitDiffPreview()
		}
	case "down", "j":
		if m.gitSel < len(m.gitFiles)-1 {
			m.gitSel++
			m.clampGitScroll()
			m.refreshGitDiffPreview()
		}
	case "space":
		if m.gitSel >= len(m.gitFiles) {
			break
		}
		fs := m.gitFiles[m.gitSel]
		r := m.repoForCur()
		if r == nil {
			break
		}
		absPath := filepath.Join(r.Root, filepath.FromSlash(fs.Path))
		if fs.IsStaged() {
			if err := r.Unstage(absPath); err != nil {
				m.msg = "unstage error: " + err.Error()
			} else {
				m.msg = "unstaged: " + fs.Path
			}
		} else {
			if err := r.Stage(absPath); err != nil {
				m.msg = "stage error: " + err.Error()
			} else {
				m.msg = "staged: " + fs.Path
			}
		}
		m.refreshGitFiles()
		m.refreshGitDiffPreview()
	case "enter":
		if m.gitSel >= len(m.gitFiles) {
			break
		}
		r := m.repoForCur()
		if r != nil {
			absPath := filepath.Join(r.Root, filepath.FromSlash(m.gitFiles[m.gitSel].Path))
			m.focusOrOpen(absPath)
		}
	case "d":
		m.openDiffView()
	case "l":
		m.openLogView()
	case "c":
		m.gitMode = gitModeCommit
		m.gitCommitIn = nil
		m.msg = ""
	case "r":
		m.refreshGitFiles()
		m.refreshGitDiffPreview()
		m.msg = "refreshed"
	case "a":
		r := m.repoForCur()
		if r == nil {
			break
		}
		for _, fs := range m.gitFiles {
			if !fs.IsStaged() {
				absPath := filepath.Join(r.Root, filepath.FromSlash(fs.Path))
				_ = r.Stage(absPath)
			}
		}
		m.refreshGitFiles()
		m.refreshGitDiffPreview()
		m.msg = fmt.Sprintf("staged all (%d files)", len(m.gitFiles))
	}
	return nil
}

// --- Commit mode ---

func (m *Model) handleGitCommit(msg tea.KeyPressMsg) tea.Cmd {
	r := m.repoForCur()
	switch msg.String() {
	case "esc":
		m.gitMode = gitModeStatus
		m.gitCommitIn = nil
		m.msg = ""
	case "enter":
		if len(m.gitCommitIn) == 0 {
			break
		}
		if r == nil {
			m.msg = "no git repo"
			break
		}
		hash, err := r.Commit(string(m.gitCommitIn))
		if err != nil {
			m.msg = "commit failed: " + err.Error()
		} else {
			m.msg = "committed: " + hash.String()[:7]
			m.gitCommitIn = nil
			m.gitMode = gitModeStatus
			m.gitOpen = false
			for i := range m.tabs {
				m.tabs[i].diffText = ""
			}
			m.refreshGitFiles()
		}
	case "backspace":
		if n := len(m.gitCommitIn); n > 0 {
			m.gitCommitIn = m.gitCommitIn[:n-1]
		}
	default:
		if len(msg.Text) > 0 {
			m.gitCommitIn = append(m.gitCommitIn, []rune(msg.Text)...)
		}
	}
	return nil
}

// --- Log mode ---

func (m *Model) handleGitLog(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()

	// Global keys
	switch key {
	case "esc", "ctrl+g":
		m.gitMode = gitModeStatus
		m.gitDiffFocused = false
		m.msg = ""
		return nil
	case "q":
		if m.gitDiffFocused {
			m.gitDiffFocused = false
			return nil
		}
		m.gitMode = gitModeStatus
		m.gitDiffFocused = false
		m.msg = ""
		return nil
	}

	// Diff area has focus — scroll
	if m.gitDiffFocused {
		h := m.viewHeight()
		switch key {
		case "left", "tab":
			m.gitDiffFocused = false
			return nil
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
		case "h":
			m.diffOffsetX -= 8
		case "right", "l":
			m.diffOffsetX += 8
		}
		m.clampDiffScroll(h)
		return nil
	}

	// Commit list has focus
	switch key {
	case "right", "tab":
		if len(m.diffRows) > 0 {
			m.gitDiffFocused = true
		}
		return nil
	case "up", "k":
		if m.gitLogSel > 0 {
			m.gitLogSel--
			m.clampGitLogScroll()
			m.showLogDiff()
		}
	case "down", "j":
		if m.gitLogSel < len(m.gitLogEntries)-1 {
			m.gitLogSel++
			m.clampGitLogScroll()
			m.showLogDiff()
		}
	case "pgup":
		m.gitLogSel -= m.viewHeight()
		if m.gitLogSel < 0 {
			m.gitLogSel = 0
		}
		m.clampGitLogScroll()
		m.showLogDiff()
	case "pgdn":
		m.gitLogSel += m.viewHeight()
		if m.gitLogSel >= len(m.gitLogEntries) {
			m.gitLogSel = maxInt(0, len(m.gitLogEntries)-1)
		}
		m.clampGitLogScroll()
		m.showLogDiff()
	case "home":
		m.gitLogSel = 0
		m.clampGitLogScroll()
		m.showLogDiff()
	case "end":
		m.gitLogSel = maxInt(0, len(m.gitLogEntries)-1)
		m.clampGitLogScroll()
		m.showLogDiff()
	case "r":
		m.refreshGitLog()
		m.showLogDiff()
		m.msg = "refreshed"
	}
	return nil
}
