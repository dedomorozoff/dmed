package editor

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"dmed/internal/vcs"
)

// gitPanelMode controls what the Git panel is showing.
type gitPanelMode int

const (
	gitModeStatus gitPanelMode = iota // file list
	gitModeCommit                     // commit message input
)

// gitPanel state is stored inside the Model fields:
//   gitOpen       bool           - panel visible
//   gitMode       gitPanelMode
//   gitFiles      []vcs.FileStatus
//   gitSel        int            - selected file index
//   gitCommitIn   []rune

func (m *Model) openGitPanel() {
	m.gitOpen = true
	m.gitMode = gitModeStatus
	m.gitCommitIn = nil
	m.refreshGitFiles()
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
		m.gitSel = max(0, len(m.gitFiles)-1)
	}
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (m *Model) handleGit(msg tea.KeyMsg) tea.Cmd {
	switch m.gitMode {
	case gitModeStatus:
		return m.handleGitStatus(msg)
	case gitModeCommit:
		return m.handleGitCommit(msg)
	}
	return nil
}

func (m *Model) handleGitStatus(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "q", "ctrl+g":
		m.gitOpen = false
		m.msg = ""
	case "up", "k":
		if m.gitSel > 0 {
			m.gitSel--
		}
	case "down", "j":
		if m.gitSel < len(m.gitFiles)-1 {
			m.gitSel++
		}
	case " ":
		// Toggle stage/unstage for selected file
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
	case "enter":
		// Open file in editor, keep panel open
		if m.gitSel >= len(m.gitFiles) {
			break
		}
		r := m.repoForCur()
		if r != nil {
			absPath := filepath.Join(r.Root, filepath.FromSlash(m.gitFiles[m.gitSel].Path))
			m.focusOrOpen(absPath)
		}
	case "c":
		// Switch to commit mode
		m.gitMode = gitModeCommit
		m.gitCommitIn = nil
		m.msg = ""
	case "r":
		// Refresh
		m.refreshGitFiles()
		m.msg = "refreshed"
	case "a":
		// Stage all
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
		m.msg = fmt.Sprintf("staged all (%d files)", len(m.gitFiles))
	}
	return nil
}

func (m *Model) handleGitCommit(msg tea.KeyMsg) tea.Cmd {
	r := m.repoForCur()
	switch msg.String() {
	case "esc":
		// Back to status
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
			m.gitOpen = false
			// Invalidate diff cache for all tabs
			for i := range m.tabs {
				m.tabs[i].diffText = ""
			}
		}
	case "backspace":
		if n := len(m.gitCommitIn); n > 0 {
			m.gitCommitIn = m.gitCommitIn[:n-1]
		}
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			m.gitCommitIn = append(m.gitCommitIn, msg.Runes...)
		}
	}
	return nil
}
