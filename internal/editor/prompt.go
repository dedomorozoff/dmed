package editor

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m *Model) startPrompt() {
	m.promptOpen = true
	m.promptIn = nil
}

func (m *Model) startNewFilePrompt() {
	m.promptOpen = true
	m.promptNewFile = true
	m.promptRenameRel = ""
	m.promptIn = nil
}

func (m *Model) startNewFolderPrompt() {
	m.promptOpen = true
	m.promptNewFolder = true
	m.promptRenameRel = ""
	m.promptIn = nil
}

func (m *Model) startTreeNewFilePrompt() {
	m.promptOpen = true
	m.promptNewFile = true
	m.promptRenameRel = ""
	m.promptIn = []rune(m.treeTargetDir())
}

func (m *Model) startTreeNewFolderPrompt() {
	m.promptOpen = true
	m.promptNewFolder = true
	m.promptRenameRel = ""
	m.promptIn = []rune(m.treeTargetDir())
}

func (m *Model) startTreeRenamePrompt(rel string) {
	m.promptOpen = true
	m.promptRename = true
	m.promptRenameRel = rel
	m.promptIn = []rune(relName(rel))
}

func (m *Model) startSavePrompt() {
	m.promptSave = true
	m.promptSaveIn = nil
}

func (m *Model) handlePrompt(s string, msg tea.KeyPressMsg) tea.Cmd {
	switch s {
	case "esc":
		m.promptOpen = false
		m.promptNewFile = false
		m.promptNewFolder = false
		m.promptRename = false
		m.promptRenameRel = ""
	case "enter":
		path := strings.TrimSpace(string(m.promptIn))
		newFolder := m.promptNewFolder
		renameRel := m.promptRenameRel
		m.promptOpen = false
		m.promptNewFile = false
		m.promptNewFolder = false
		m.promptRename = false
		m.promptRenameRel = ""
		if path == "" {
			break
		}
		switch {
		case renameRel != "":
			m.renameTreeEntry(renameRel, path)
		case newFolder:
			full := normalizePath(m.baseDir(), path)
			if err := os.MkdirAll(full, 0o755); err != nil {
				m.msg = m.t("msg.create_folder_fail", err.Error())
			} else {
				m.msg = m.t("msg.created_folder", filepath.Base(full))
			}
			m.rebuildTree()
			m.refreshGitFiles()
		default:
			m.openPath(path)
			m.rebuildTree()
			m.refreshGitFiles()
		}
	case "backspace":
		if n := len(m.promptIn); n > 0 {
			m.promptIn = m.promptIn[:n-1]
		}
	default:
		if len(msg.Text) > 0 {
			m.promptIn = append(m.promptIn, []rune(msg.Text)...)
		}
	}
	return nil
}

func (m *Model) handleSavePrompt(s string, msg tea.KeyPressMsg) tea.Cmd {
	switch s {
	case "esc":
		m.promptSave = false
		m.pendingQuit = false
	case "enter":
		raw := strings.TrimSpace(string(m.promptSaveIn))
		m.promptSave = false
		if raw != "" {
			path := normalizePath(m.baseDir(), raw)
			t := m.cur()
			t.path = path
			if m.watcher != nil {
				if err := m.watcher.Watch(path); err != nil {
					m.msg = m.t("msg.watch_failed", path, err.Error())
				}
			}
			text := t.buf.Text()
			if t.lineEnding == "crlf" {
				text = strings.ReplaceAll(text, "\n", "\r\n")
			}
			if err := os.WriteFile(t.path, []byte(text), 0o644); err != nil {
				m.msg = m.t("msg.save_failed", err.Error())
				return nil
			}
			t.buf.MarkSaved()
			m.msg = m.t("msg.saved")
			if m.pendingQuit {
				m.pendingQuit = false
				m.shutdown()
				return tea.Quit
			}
		}
	case "backspace":
		if n := len(m.promptSaveIn); n > 0 {
			m.promptSaveIn = m.promptSaveIn[:n-1]
		}
	default:
		if len(msg.Text) > 0 {
			m.promptSaveIn = append(m.promptSaveIn, []rune(msg.Text)...)
		}
	}
	return nil
}

// startGotoPrompt opens the "Go to Line" input (empty; type N, N:C, or +N/-N).
func (m *Model) startGotoPrompt() {
	m.gotoOpen = true
	m.gotoIn = nil
}

// handleGoto handles input in the "Go to Line" prompt. Accepts absolute
// (N, or N:C) and relative (+N/-N, or +N:C/-N:C) line specs.
func (m *Model) handleGoto(s string, msg tea.KeyPressMsg) tea.Cmd {
	switch s {
	case "esc":
		m.gotoOpen = false
		m.msg = ""
	case "enter":
		s := strings.TrimSpace(string(m.gotoIn))
		m.gotoOpen = false
		m.msg = ""
		if s != "" {
			m.applyGoto(s)
		}
	case "backspace":
		if n := len(m.gotoIn); n > 0 {
			m.gotoIn = m.gotoIn[:n-1]
		}
	case "ctrl+l":
		m.gotoIn = nil
	default:
		if len(msg.Text) > 0 {
			for _, r := range msg.Text {
				if (r >= '0' && r <= '9') || r == ':' || r == '+' || r == '-' {
					m.gotoIn = append(m.gotoIn, r)
				}
			}
		}
	}
	return nil
}

// applyGoto jumps the active buffer's cursor to the parsed line spec.
func (m *Model) applyGoto(s string) {
	t := m.cur()
	cur := t.buf.CurLine()

	rel := false
	sign := 1
	rest := s
	switch rest[0] {
	case '+':
		rel = true
		rest = rest[1:]
	case '-':
		rel = true
		sign = -1
		rest = rest[1:]
	}

	col := 0
	if i := strings.IndexByte(rest, ':'); i >= 0 {
		col, _ = strconv.Atoi(rest[i+1:])
		if col < 0 {
			col = 0
		}
		rest = rest[:i]
	}
	if len(rest) == 0 {
		return
	}
	n, err := strconv.Atoi(rest)
	if err != nil {
		return
	}
	line := n - 1
	if rel {
		line = cur + sign*n
	}
	if line < 0 {
		line = 0
	}
	if line >= t.buf.LineCount() {
		line = t.buf.LineCount() - 1
	}
	t.buf.SetCursor(line, 0)
	if col > 0 {
		if col > t.buf.LineLen(line) {
			col = t.buf.LineLen(line)
		}
		t.buf.SetCursor(line, col)
	}
	m.clampScroll()
	m.msg = fmt.Sprintf("line %d", line+1)
}
