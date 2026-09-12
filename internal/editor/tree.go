package editor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"dmed/internal/buffer"
)

var (
	treeConnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	treeIconStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("208"))
	treeDirStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true)
	treeFileStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("253"))
)

const (
	treeMaxRows  = 1000
	treeMinWidth = 36
)

type treeEntry struct {
	rel   string
	name  string
	depth int
	isDir bool
	anc   []bool // 'last sibling' flag of each ancestor, nearest parent last
	last  bool   // true when this entry is the last child of its parent
}

func (m Model) sidebarOn() bool {
	return m.treeVisible && m.width >= treeMinWidth
}

func (m *Model) toggleTree() {
	if m.gitOpen {
		m.gitOpen = false
		m.gitFocus = false
		m.msg = ""
	}
	if !m.treeVisible {
		m.treeVisible = true
		m.treeFocus = true
		m.gitFocus = false
		m.chatFocus = false
		m.rebuildTree()
		return
	}
	if m.treeFocus {
		m.treeVisible = false
		m.treeFocus = false
		m.gitFocus = false
		m.chatFocus = false
		return
	}
	m.treeFocus = true
	m.gitFocus = false
	m.chatFocus = false
}

func (m *Model) rebuildTree() {
	m.treeRows = m.buildTree()
	if m.treeSel >= len(m.treeRows) {
		m.treeSel = len(m.treeRows) - 1
	}
	if m.treeSel < 0 {
		m.treeSel = 0
	}
}

func (m *Model) buildTree() []treeEntry {
	base := m.baseDir()
	var rows []treeEntry
	var walk func(rel string, depth int, anc []bool)
	walk = func(rel string, depth int, anc []bool) {
		dir := filepath.Join(base, filepath.FromSlash(rel))
		dents, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		sort.Slice(dents, func(i, j int) bool {
			di, dj := dents[i].IsDir(), dents[j].IsDir()
			if di != dj {
				return di
			}
			return dents[i].Name() < dents[j].Name()
		})
		for i, d := range dents {
			name := d.Name()
			skip := false
			for _, s := range m.cfg.Editor.SkippedDirs {
				if name == s {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
			childRel := name
			if rel != "" {
				childRel = rel + "/" + name
			}
			rows = append(rows, treeEntry{
				rel:   childRel,
				name:  name,
				depth: depth,
				isDir: d.IsDir(),
				anc:   anc,
				last:  i == len(dents)-1,
			})
			if len(rows) >= treeMaxRows {
				return
			}
			if d.IsDir() && m.expanded[childRel] {
				childAnc := make([]bool, len(anc)+1)
				copy(childAnc, anc)
				childAnc[len(anc)] = i == len(dents)-1
				walk(childRel, depth+1, childAnc)
				if len(rows) >= treeMaxRows {
					return
				}
			}
		}
	}
	walk("", 1, nil)
	return rows
}

// treeEntryRows is the number of tree rows that fit in a panel of height h
// before the key-hint lines.
func (m Model) treeEntryRows(h int) int {
	if h <= 0 {
		return 0
	}
	inner := m.cfg.UI.TreeWidth - 2
	if inner < 1 {
		inner = 1
	}
	er := h - len(m.treeHint(inner))
	if er < 0 {
		er = 0
	}
	return er
}

func (m *Model) clampTreeScroll(h int) {
	if h <= 0 {
		return
	}
	n := len(m.treeRows)
	if m.treeSel < m.treeOffset {
		m.treeOffset = m.treeSel
	}
	if m.treeSel >= m.treeOffset+h {
		m.treeOffset = m.treeSel - h + 1
	}
	if m.treeOffset > n-h {
		m.treeOffset = n - h
	}
	if m.treeOffset < 0 {
		m.treeOffset = 0
	}
}

// clampedTreeOffset returns the scroll offset clamped to the visible window,
// so a stale offset (e.g. after a resize) never blanks the start of the tree.
func (m Model) clampedTreeOffset(h int) int {
	if h <= 0 {
		return 0
	}
	n := len(m.treeRows)
	if n <= h {
		return 0
	}
	if m.treeOffset < 0 {
		return 0
	}
	if m.treeOffset > n-h {
		return n - h
	}
	return m.treeOffset
}

func (m *Model) handleTree(msg tea.KeyPressMsg) tea.Cmd {
	if m.treeConfirm != "" {
		return m.handleTreeConfirm(msg)
	}
	switch msg.String() {
	case "up":
		if m.treeSel > 0 {
			m.treeSel--
		}
	case "down":
		if m.treeSel < len(m.treeRows)-1 {
			m.treeSel++
		}
	case "pgup":
		m.treeSel -= m.viewHeight()
		if m.treeSel < 0 {
			m.treeSel = 0
		}
	case "pgdown":
		m.treeSel += m.viewHeight()
		if m.treeSel > len(m.treeRows)-1 {
			m.treeSel = len(m.treeRows) - 1
		}
	case "enter":
		if m.treeSel >= len(m.treeRows) {
			break
		}
		e := m.treeRows[m.treeSel]
		if e.isDir {
			// Toggle expand/collapse
			if m.expanded[e.rel] {
				delete(m.expanded, e.rel)
			} else {
				m.expanded[e.rel] = true
			}
			m.rebuildTree()
		} else {
			// Open file and return focus to editor (tree stays open)
			m.focusOrOpen(e.rel)
			m.treeFocus = false
		}
	case "right":
		if m.treeSel >= len(m.treeRows) {
			break
		}
		e := m.treeRows[m.treeSel]
		if e.isDir {
			m.expanded[e.rel] = true
			m.rebuildTree()
		} else {
			// Preview: open but keep tree focused
			m.focusOrOpen(e.rel)
		}
	case "left":
		if m.treeSel >= len(m.treeRows) {
			break
		}
		e := m.treeRows[m.treeSel]
		if e.isDir && m.expanded[e.rel] {
			delete(m.expanded, e.rel)
			m.rebuildTree()
		} else if !e.isDir || !m.expanded[e.rel] {
			if i := strings.LastIndex(e.rel, "/"); i > 0 {
				parent := e.rel[:i]
				for j, r := range m.treeRows {
					if r.rel == parent {
						m.treeSel = j
						break
					}
				}
			}
		}
	case "esc", "tab":
		// Return focus to editor without closing the sidebar
		m.treeFocus = false
	case "ctrl+g":
		// Switch to the Git panel (tree stays visible, loses focus)
		m.treeFocus = false
		m.openGitPanel()
		return nil
	case "ctrl+b", "f9":
		m.toggleTree()
	case "n":
		m.startTreeNewFilePrompt()
	case "N":
		m.startTreeNewFolderPrompt()
	case "r":
		if rel, ok := m.treeSelectedRel(); ok {
			m.startTreeRenamePrompt(rel)
		}
	case "d":
		if rel, ok := m.treeSelectedRel(); ok {
			m.duplicateTreeEntry(rel)
			m.rebuildTree()
			m.refreshGitFiles()
		}
	case "delete":
		if rel, ok := m.treeSelectedRel(); ok {
			m.beginTreeConfirm("delete", rel)
		}
	case "t":
		if rel, ok := m.treeSelectedRel(); ok {
			m.beginTreeConfirm("trash", rel)
		}
	default:
		return nil
	}
	m.clampTreeScroll(m.treeEntryRows(m.viewHeight()))
	return nil
}

// handleTreeConfirm processes Y/N/Esc while a delete/trash confirmation is
// pending on the selected tree entry. It is layout-independent (н/т are the
// physical Y/N keys in the Cyrillic layout).
func (m *Model) handleTreeConfirm(msg tea.KeyPressMsg) tea.Cmd {
	rel := m.treeConfirmRel
	full := normalizePath(m.baseDir(), rel)
	confirm := m.treeConfirm
	switch gitKeyName(msg) {
	case "y", "enter":
		var err error
		if confirm == "trash" {
			err = moveToTrash(full)
			if err == nil {
				m.msg = m.t("msg.trashed", rel)
			} else {
				m.msg = m.t("msg.trash_failed", err.Error())
			}
		} else {
			err = os.RemoveAll(full)
			if err == nil {
				m.msg = m.t("msg.deleted", rel)
			} else {
				m.msg = m.t("msg.delete_failed", err.Error())
			}
		}
		if err == nil {
			m.dropTabsUnder(full)
		}
	case "n", "esc":
		m.msg = m.t("msg.kept")
	default:
		return nil
	}
	m.treeConfirm = ""
	m.treeConfirmRel = ""
	m.rebuildTree()
	m.refreshGitFiles()
	return nil
}

// beginTreeConfirm arms a delete/trash confirmation for the given entry.
func (m *Model) beginTreeConfirm(kind, rel string) {
	m.treeConfirm = kind
	m.treeConfirmRel = rel
	m.msg = ""
}

// treeSelectedRel returns the relative path of the selected entry, if any.
func (m *Model) treeSelectedRel() (string, bool) {
	if m.treeSel < 0 || m.treeSel >= len(m.treeRows) {
		return "", false
	}
	return m.treeRows[m.treeSel].rel, true
}

// treeTargetDir returns the directory (relative to baseDir, trailing "/")
// that new files/folders should be created in based on the selected entry:
// the entry itself if it is a directory, otherwise its parent directory.
func (m *Model) treeTargetDir() string {
	rel, ok := m.treeSelectedRel()
	if !ok {
		return ""
	}
	if m.treeRows[m.treeSel].isDir {
		return rel + "/"
	}
	if i := strings.LastIndex(rel, "/"); i > 0 {
		return rel[:i+1]
	}
	return ""
}

// relName returns the base name of a slash-separated relative path.
func relName(rel string) string {
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[i+1:]
	}
	return rel
}

// renameTreeEntry renames the entry at fromRel to the full path in toPath
// (relative or absolute, normalized against baseDir). Open tabs and watcher
// registrations are updated to follow the move.
func (m *Model) renameTreeEntry(fromRel, toPath string) {
	fullFrom := normalizePath(m.baseDir(), fromRel)
	fullTo := normalizePath(m.baseDir(), toPath)
	if fullFrom == fullTo {
		return
	}
	if m.pathExists(fullTo) {
		m.msg = m.t("msg.already_exists", toPath)
		return
	}
	if dir := filepath.Dir(fullTo); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			m.msg = m.t("msg.rename_failed", err.Error())
			return
		}
	}
	if err := os.Rename(fullFrom, fullTo); err != nil {
		m.msg = m.t("msg.rename_failed", err.Error())
		return
	}
	// Follow open tabs and watcher registrations to the new path.
	for i := range m.tabs {
		t := &m.tabs[i]
		if t.path == "" {
			continue
		}
		absT, _ := filepath.Abs(t.path)
		if absT == fullFrom || strings.HasPrefix(absT, fullFrom+string(filepath.Separator)) {
			rel := strings.TrimPrefix(absT, fullFrom)
			t.path = fullTo + rel
			if m.watcher != nil {
				_ = m.watcher.Watch(t.path)
			}
		}
	}
	// Remap expanded directories that moved with the rename.
	fromSlash := filepath.ToSlash(fromRel)
	if fromSlash == "" || strings.HasSuffix(fromSlash, "/") {
		m.expanded = map[string]bool{}
	} else {
		for k := range m.expanded {
			if k == fromSlash || strings.HasPrefix(k, fromSlash+"/") {
				delete(m.expanded, k)
			}
		}
	}
	m.rebuildTree()
	m.refreshGitFiles()
	m.msg = m.t("msg.renamed", toPath)
}

// duplicateTreeEntry copies the selected file to "<name>_copy<ext>", avoiding
// name collisions with a numeric suffix.
func (m *Model) duplicateTreeEntry(rel string) {
	full := normalizePath(m.baseDir(), rel)
	st, err := os.Stat(full)
	if err != nil {
		m.msg = m.t("msg.duplicate_failed", err.Error())
		return
	}
	if st.IsDir() {
		m.msg = m.t("msg.duplicate_failed", "not a file")
		return
	}
	dir := filepath.Dir(full)
	name := relName(rel)
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	target := filepath.Join(dir, base+"_copy"+ext)
	for i := 1; m.pathExists(target); i++ {
		target = filepath.Join(dir, fmt.Sprintf("%s_copy%d%s", base, i, ext))
	}
	data, err := os.ReadFile(full)
	if err != nil {
		m.msg = m.t("msg.duplicate_failed", err.Error())
		return
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		m.msg = m.t("msg.duplicate_failed", err.Error())
		return
	}
	m.msg = m.t("msg.duplicated", filepath.ToSlash(target))
}

// pathExists reports whether a normalized absolute path exists on disk.
func (m *Model) pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// dropTabsUnder closes any tab whose file lives at or under the given path
// (used after a file/folder was deleted or moved to trash).
func (m *Model) dropTabsUnder(full string) {
	abs, _ := filepath.Abs(full)
	for i := len(m.tabs) - 1; i >= 0; i-- {
		t := m.tabs[i]
		if t.path == "" {
			continue
		}
		abst, _ := filepath.Abs(t.path)
		if abst == abs || strings.HasPrefix(abst, abs+string(filepath.Separator)) {
			m.tabs = append(m.tabs[:i], m.tabs[i+1:]...)
			m.fixPaneTabsAfterClose(i)
		}
	}
	if len(m.tabs) == 0 {
		m.tabs = []tab{{buf: buffer.New()}}
		m.initPanes()
	}
}
