package editor

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type commandItem struct {
	id     string
	title  string
	desc   string
	action func(m *Model) tea.Cmd
}

func (m *Model) getPaletteCommands() []commandItem {
	return []commandItem{
		{id: "save", title: "File: Save", desc: "Ctrl+S — Save active buffer", action: func(m *Model) tea.Cmd { m.saveActive(); return nil }},
		{id: "save_as", title: "File: Save As...", desc: "Save active buffer to a new path", action: func(m *Model) tea.Cmd { m.startSavePrompt(); return nil }},
		{id: "close_tab", title: "File: Close Tab", desc: "Ctrl+W — Close active tab", action: func(m *Model) tea.Cmd { return m.closeTab() }},
		{id: "open", title: "File: Open by Path...", desc: "Ctrl+T — Open file prompt", action: func(m *Model) tea.Cmd { m.startPrompt(); return nil }},
		{id: "finder", title: "File: Fuzzy Finder...", desc: "Ctrl+O — Quick file search", action: func(m *Model) tea.Cmd { m.startFinder(); return nil }},
		{id: "search", title: "Edit: Find in File...", desc: "Ctrl+F — Search text", action: func(m *Model) tea.Cmd { m.startSearch(); return nil }},
		{id: "replace", title: "Edit: Replace in File...", desc: "Ctrl+H — Find and replace", action: func(m *Model) tea.Cmd { m.startReplace(); return nil }},
		{id: "undo", title: "Edit: Undo", desc: "Ctrl+Z — Undo change", action: func(m *Model) tea.Cmd { m.cur().buf.Undo(); return nil }},
		{id: "redo", title: "Edit: Redo", desc: "Ctrl+Y — Redo change", action: func(m *Model) tea.Cmd { m.cur().buf.Redo(); return nil }},
		{id: "git_commit", title: "Git: Commit Panel", desc: "Ctrl+G — Stage & commit", action: func(m *Model) tea.Cmd { m.gitOpen = true; return nil }},
		{id: "git_next", title: "Git: Next Hunk", desc: "Alt+] — Jump next hunk", action: func(m *Model) tea.Cmd { m.jumpHunk(1); return nil }},
		{id: "git_prev", title: "Git: Prev Hunk", desc: "Alt+[ — Jump prev hunk", action: func(m *Model) tea.Cmd { m.jumpHunk(-1); return nil }},
		{id: "split_v", title: "View: Split Vertical", desc: "Ctrl+\\ — Side-by-side split", action: func(m *Model) tea.Cmd { m.splitVert(); return nil }},
		{id: "split_h", title: "View: Split Horizontal", desc: "Ctrl+Alt+H — Stacked split", action: func(m *Model) tea.Cmd { m.splitHoriz(); return nil }},
		{id: "pane_focus", title: "View: Focus Other Pane", desc: "Ctrl+Alt+P — Switch pane", action: func(m *Model) tea.Cmd { m.focusOtherPane(); return nil }},
		{id: "pane_close", title: "View: Close Current Pane", desc: "Ctrl+Alt+W — Unsplit", action: func(m *Model) tea.Cmd { m.closePane(); return nil }},
		{id: "tree_toggle", title: "View: Toggle Project Tree", desc: "Ctrl+B — Sidebar tree", action: func(m *Model) tea.Cmd { m.toggleTree(); return nil }},
		{id: "help", title: "Help: Show Keybindings", desc: "F1 / Ctrl+E — Help panel", action: func(m *Model) tea.Cmd { m.helpOpen = true; return nil }},
		{id: "quit", title: "App: Quit Editor", desc: "Ctrl+Q — Exit", action: func(m *Model) tea.Cmd { return tea.Quit }},
	}
}

func (m *Model) filterPalette() []commandItem {
	all := m.getPaletteCommands()
	q := strings.ToLower(string(m.paletteQ))
	if q == "" {
		return all
	}
	var res []commandItem
	for _, c := range all {
		if strings.Contains(strings.ToLower(c.title), q) || strings.Contains(strings.ToLower(c.desc), q) {
			res = append(res, c)
		}
	}
	return res
}

func (m *Model) handlePalette(msg tea.KeyMsg) tea.Cmd {
	hits := m.filterPalette()
	switch msg.String() {
	case "esc":
		m.paletteOpen = false
		m.msg = ""
	case "enter":
		if len(hits) > 0 {
			if m.paletteSel >= len(hits) {
				m.paletteSel = 0
			}
			sel := hits[m.paletteSel]
			m.paletteOpen = false
			return sel.action(m)
		}
		m.paletteOpen = false
	case "up":
		if len(hits) > 0 {
			m.paletteSel = (m.paletteSel - 1 + len(hits)) % len(hits)
		}
	case "down":
		if len(hits) > 0 {
			m.paletteSel = (m.paletteSel + 1) % len(hits)
		}
	case "backspace":
		if len(m.paletteQ) > 0 {
			m.paletteQ = m.paletteQ[:len(m.paletteQ)-1]
			m.paletteSel = 0
		}
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			m.paletteQ = append(m.paletteQ, msg.Runes...)
			m.paletteSel = 0
		}
	}
	return nil
}
