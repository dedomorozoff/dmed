package editor

import (
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/buffer"
)

func (m *Model) openPath(rawPath string) {
	path := normalizePath(m.baseDir(), rawPath)
	// Refuse to load obvious binary/non-text files as text buffers: rendering
	// them as text corrupts the UI. Images and SQLite files have their own
	// dedicated open paths below.
	if !looksLikeImage(path) && !sqlitePathCandidate(path) {
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			if !isPlausibleText(path) {
				m.msg = "binary file not supported"
				return
			}
		}
	}
	if sqlitePathCandidate(path) {
		if err := m.tryOpenSQLite(path); err == nil {
			m.msg = m.t("msg.sqlite_opened", filepath.Base(path))
			return
		}
		// Not (readable as) SQLite: fall through to the plain text open.
	}
	data, err := os.ReadFile(path)
	t := tab{path: path, buf: buffer.New(), lineEnding: "lf", encoding: "utf-8"}
	if err != nil {
		if os.IsNotExist(err) {
			m.msg = m.t("msg.new_file", path)
		} else {
			m.msg = m.t("msg.open_failed", err.Error())
		}
	} else {
		le, enc := detectFileInfo(data)
		t.lineEnding = le
		t.encoding = enc
		// A picture is opened as a picture, and only if the decoder agrees: a
		// .png that is not one falls through to the text view below with the
		// reason in the status line, because a tab full of mojibake and no
		// explanation is worse than either. The extension is the cheap first
		// question and the bytes are the real one, so a picture named without
		// a suffix still opens as text — attempting a decode on every file the
		// editor opens is the wrong question to ask of a log.
		if looksLikeImage(path) {
			if v, err2 := loadImage(data); err2 == nil {
				t.img = v
			} else {
				m.msg = m.t("msg.image_failed", err2.Error())
			}
		}
		if t.img == nil {
			t.buf = buffer.Load(normalizePaste(string(data)))
		}
	}
	if m.watcher != nil && path != "" {
		if err := m.watcher.Watch(path); err != nil {
			// Watching is best-effort, but say so: a silent failure here looks
			// exactly like "external changes are not detected".
			m.msg = m.t("msg.watch_failed", path, err.Error())
		}
	}
	m.tabs = append(m.tabs, t)
	// Only set active tab if panes are already initialized
	if len(m.panes) > 0 {
		m.setActiveTab(len(m.tabs) - 1)
	}
	if m.plugins != nil {
		m.plugins.Emit(m, "file_open")
	}
	// Hint when this file's language needs an LSP server that isn't installed.
	// A picture asks no LSP questions: the buffer behind it is empty by design.
	if err == nil && t.img == nil {
		if hint := m.lspMissingHintFor(t.path); hint != "" {
			m.msg = m.t("msg.lsp_missing", hint)
		}
	}
}

func (m *Model) switchTab(d int) {
	n := len(m.tabs)
	if n == 0 {
		return
	}
	idx := m.activeTabIndex()
	m.setActiveTab(((idx+d)%n + n) % n)
}

func (m *Model) jumpTab(n int) {
	m.setActiveTab(n)
}

func (m *Model) closeTab() tea.Cmd {
	return m.closeTabAt(m.activeTabIndex())
}

// closeTabAt closes the tab at the given index (used by middle-click on the
// tab bar, which targets a specific tab rather than the active one).
func (m *Model) closeTabAt(idx int) tea.Cmd {
	if len(m.tabs) == 1 {
		return m.requestQuit()
	}
	if idx < 0 || idx >= len(m.tabs) {
		return nil
	}
	if m.layout != splitNone {
		// Closing a tab in a split also collapses the split.
		other := 1 - m.activePane
		m.panes = []pane{m.panes[other]}
		m.activePane = 0
		m.layout = splitNone
	}
	m.tabs = append(m.tabs[:idx], m.tabs[idx+1:]...)
	m.fixPaneTabsAfterClose(idx)
	return nil
}

func (m *Model) focusOrOpen(rawPath string) {
	path := normalizePath(m.baseDir(), rawPath)
	for i := range m.tabs {
		if m.tabs[i].path == path {
			m.setActiveTab(i)
			return
		}
	}
	m.openPath(path)
}

func (m Model) hasDirty() bool {
	for _, t := range m.tabs {
		if t.buf.Dirty() {
			return true
		}
	}
	return false
}

func (m *Model) handleQuitConfirm(s string, msg tea.KeyPressMsg) tea.Cmd {
	// keyOf names the physical key, so the y/n answer works in any keyboard
	// layout (н/т are the Y/N keys on ЙЦУКЕН).
	switch s {
	case "esc":
		m.quitConfirm = false
		m.pendingQuit = false
		m.quitTab = false
	case "y", "Y":
		t := m.cur()
		if t.path == "" {
			m.pendingQuit = true
			m.quitConfirm = false
			m.startSavePrompt()
			return nil
		}
		m.saveActive()
		if t.buf.Dirty() {
			m.quitConfirm = false
			m.pendingQuit = false
			m.quitTab = false
			m.msg = m.t("msg.save_failed_gen")
			if m.quitTab {
				m.quitTab = false
			}
			return nil
		}
		m.quitConfirm = false
		m.pendingQuit = false
		m.quitTab = false
		if m.quitTab {
			// Just close the tab after saving
			cmd := m.closeTab()
			if cmd == nil {
				return nil
			}
			m.shutdown()
			return cmd
		}
		m.shutdown()
		return tea.Quit
	case "n", "N":
		m.quitConfirm = false
		m.pendingQuit = false
		m.quitTab = false
		if m.quitTab {
			// Close the tab without saving
			cmd := m.closeTab()
			if cmd == nil {
				return nil
			}
			m.shutdown()
			return cmd
		}
		m.shutdown()
		return tea.Quit
	}
	return nil
}

func (m *Model) requestQuit() tea.Cmd {
	if m.hasDirty() {
		m.quitConfirm = true
		return nil
	}
	m.shutdown()
	return tea.Quit
}

// shutdown stops background processes and persists the session.
func (m *Model) shutdown() {
	m.cancelChat()
	m.killTerminal()
	if m.agentCancel != nil {
		m.agentCancel()
		m.agentCancel = nil
	}
	if m.watcher != nil {
		_ = m.watcher.Close()
	}
	if m.lspClient != nil {
		m.lspClient.Close()
	}
	if m.dapClient != nil {
		m.dapClient.Close()
	}
	m.saveSession()
}

// closeActiveTab closes the tab of the active pane; closing the last tab
// quits (with a save confirmation when the buffer is dirty).
func (m *Model) closeActiveTab() tea.Cmd {
	t := m.cur()
	if t == nil {
		return nil
	}

	// If this is the last tab, quit (with save prompt if dirty)
	if len(m.tabs) == 1 {
		if t.buf.Dirty() {
			m.quitConfirm = true
			m.pendingQuit = true
			m.quitTab = false // This is a quit, not a tab close
			return nil
		}
		m.shutdown()
		return tea.Quit
	}

	// Multiple tabs: check if the tab being closed is dirty
	if t.buf.Dirty() {
		m.quitTab = true
		m.pendingQuit = true
		m.quitConfirm = true
		return nil
	}

	// Tab is clean, just close it
	cmd := m.closeTab()
	if cmd == nil {
		return nil
	}
	m.shutdown()
	return cmd
}

func (m *Model) saveActive() {
	t := m.cur()
	if t.path == "" {
		m.msg = m.t("msg.cannot_save")
		return
	}
	text := t.buf.Text()
	if t.lineEnding == "crlf" {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}
	if err := os.WriteFile(t.path, []byte(text), 0o644); err != nil {
		m.msg = m.t("msg.save_failed", err.Error())
		return
	}
	t.buf.MarkSaved()
	m.msg = m.t("msg.saved")
	if m.plugins != nil {
		m.plugins.Emit(m, "save")
	}
}
