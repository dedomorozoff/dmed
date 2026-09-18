package editor

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/config"
	"dmed/internal/i18n"
	"dmed/internal/vcs"
)

// folderEntry is one row inside the TUI folder browser.
type folderEntry struct {
	name string
	dir  bool
}

// folderVisible is how many rows the browser viewport shows before scrolling.
const folderVisible = 12

// startFolderBrowser opens the built-in folder picker at the project root.
// It works offline on every platform, unlike spawning zenity/kdialog (Linux)
// or PowerShell (Windows), and is what "File: Open Folder..." runs.
func (m *Model) startFolderBrowser() {
	m.folderOpen = true
	m.folderSel = 0
	m.folderOffset = 0
	m.folderPath = filepath.Clean(m.baseDir())
	if m.folderPath == "." {
		if abs, err := os.Getwd(); err == nil {
			m.folderPath = filepath.Clean(abs)
		}
	}
	m.folderRefresh()
}

// folderRefresh reloads the visible directory, keeping the selection valid.
func (m *Model) folderRefresh() {
	m.folderEntries = readFolderEntries(m.folderPath)
	if m.folderSel > len(m.folderEntries) {
		m.folderSel = len(m.folderEntries)
	}
	m.clampFolder()
}

// readFolderEntries lists dir, directories first, both case-insensitively.
// Row 0 of the browser is always the virtual ".." parent entry, so the first
// real entry maps to selection index 1.
func readFolderEntries(dir string) []folderEntry {
	dents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]folderEntry, 0, len(dents))
	for _, d := range dents {
		out = append(out, folderEntry{name: d.Name(), dir: d.IsDir()})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].dir != out[j].dir {
			return out[i].dir
		}
		return strings.ToLower(out[i].name) < strings.ToLower(out[j].name)
	})
	return out
}

// folderUp moves the browser to the parent directory.
func (m *Model) folderUp() {
	parent := filepath.Dir(m.folderPath)
	if filepath.Clean(parent) == filepath.Clean(m.folderPath) {
		return // filesystem root
	}
	m.folderPath = filepath.Clean(parent)
	m.folderSel = 0
	m.folderOffset = 0
	m.folderRefresh()
}

// folderEnter descends into the selected directory (row 0 is ".."), or
// ascends when ".." is selected.
func (m *Model) folderEnter() {
	if m.folderSel == 0 {
		m.folderUp()
		return
	}
	idx := m.folderSel - 1
	if idx < 0 || idx >= len(m.folderEntries) {
		return
	}
	e := m.folderEntries[idx]
	if !e.dir {
		return // files are shown for orientation but cannot be opened here
	}
	m.folderPath = filepath.Clean(filepath.Join(m.folderPath, e.name))
	m.folderSel = 0
	m.folderOffset = 0
	m.folderRefresh()
}

// folderUse switches the project root to the folder currently being browsed
// and closes the picker.
func (m *Model) folderUse() {
	if m.folderPath == "" {
		return
	}
	m.folderOpen = false
	m.folderSel = 0
	m.folderOffset = 0
	m.switchRoot(m.folderPath)
}

// handleFolderBrowser routes keys while the built-in folder picker is open.
func (m *Model) handleFolderBrowser(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.folderOpen = false
	case "enter", "right":
		m.folderEnter()
	case "left", "backspace":
		m.folderUp()
	case "o":
		m.folderUse()
	case "up":
		if m.folderSel > 0 {
			m.folderSel--
			m.clampFolder()
		}
	case "down":
		if m.folderSel < len(m.folderEntries) {
			m.folderSel++
			m.clampFolder()
		}
	case "pgup":
		m.folderSel -= folderVisible
		if m.folderSel < 0 {
			m.folderSel = 0
		}
		m.clampFolder()
	case "pgdown":
		m.folderSel += folderVisible
		if m.folderSel > len(m.folderEntries) {
			m.folderSel = len(m.folderEntries)
		}
		m.clampFolder()
	case "home":
		m.folderSel = 0
		m.folderOffset = 0
	case "end":
		m.folderSel = len(m.folderEntries)
		m.clampFolder()
	case "~":
		if home, err := os.UserHomeDir(); err == nil {
			m.folderPath = filepath.Clean(home)
			m.folderSel = 0
			m.folderOffset = 0
			m.folderRefresh()
		}
	case "/":
		m.folderPath = filepath.Clean(filepath.VolumeName(m.folderPath) + string(filepath.Separator))
		m.folderSel = 0
		m.folderOffset = 0
		m.folderRefresh()
	}
	return nil
}

// clampFolder keeps the browser viewport scrolled so folderSel stays visible,
// mirroring clampPalette.
func (m *Model) clampFolder() {
	if m.folderSel <= folderVisible {
		m.folderOffset = 0
		return
	}
	if m.folderSel > len(m.folderEntries)-folderVisible {
		m.folderOffset = len(m.folderEntries) - folderVisible
		m.folderSel = m.folderOffset
		return
	}
	if m.folderSel < m.folderOffset {
		m.folderOffset = m.folderSel
	}
	if m.folderSel > m.folderOffset+folderVisible {
		m.folderOffset = m.folderSel - folderVisible
	}
}

// switchRoot repoints the project root at path and re-initializes the
// root-derived state: config, UI language, project tree, git repo, file
// watcher and project plugins.
func (m *Model) switchRoot(path string) {
	m.root = normalizePath(".", path)
	m.cfg = config.Load(m.root)
	m.tr = i18n.New(i18n.Resolve(m.cfg.UI.Lang))
	m.treeVisible = true
	m.rebuildTree()
	if repo, err := vcs.Open(m.baseDir()); err == nil {
		m.repo = repo
	}
	if m.watcher != nil {
		m.watcher.Watch(m.root)
	}
	m.loadPlugins()
	m.msg = m.t("msg.project", filepath.Base(m.root))
}