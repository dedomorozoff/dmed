package editor

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
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
}

func (m Model) sidebarOn() bool {
	return m.treeVisible && m.width >= treeMinWidth
}

func (m *Model) toggleTree() {
	if m.gitOpen {
		m.gitOpen = false
		m.msg = ""
	}
	if !m.treeVisible {
		m.treeVisible = true
		m.treeFocus = true
		m.rebuildTree()
		return
	}
	if m.treeFocus {
		m.treeVisible = false
		m.treeFocus = false
		return
	}
	m.treeFocus = true
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
	var walk func(rel string, depth int)
	walk = func(rel string, depth int) {
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
		for _, d := range dents {
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
			rows = append(rows, treeEntry{rel: childRel, name: name, depth: depth, isDir: d.IsDir()})
			if len(rows) >= treeMaxRows {
				return
			}
			if d.IsDir() && m.expanded[childRel] {
				walk(childRel, depth+1)
				if len(rows) >= treeMaxRows {
					return
				}
			}
		}
	}
	walk("", 1)
	return rows
}

func (m *Model) clampTreeScroll(h int) {
	if h <= 0 {
		return
	}
	if m.treeSel < m.treeOffset {
		m.treeOffset = m.treeSel
	}
	if m.treeSel >= m.treeOffset+h {
		m.treeOffset = m.treeSel - h + 1
	}
	if m.treeOffset > len(m.treeRows)-h {
		m.treeOffset = len(m.treeRows) - h
	}
	if m.treeOffset < 0 {
		m.treeOffset = 0
	}
}

func (m *Model) handleTree(msg tea.KeyPressMsg) tea.Cmd {
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
	default:
		return nil
	}
	m.clampTreeScroll(m.viewHeight())
	return nil
}
