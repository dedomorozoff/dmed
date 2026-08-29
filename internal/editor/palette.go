package editor

import (
	"strings"

	tea "charm.land/bubbletea/v2"
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
		{id: "new_file", title: "File: New File...", desc: "Create a new file by path", action: func(m *Model) tea.Cmd { m.startNewFilePrompt(); return nil }},
		{id: "finder", title: "File: Fuzzy Finder...", desc: "Ctrl+O — Quick file search", action: func(m *Model) tea.Cmd { m.startFinder(); return nil }},
		{id: "search", title: "Edit: Find in File...", desc: "Ctrl+F — Search text", action: func(m *Model) tea.Cmd { m.startSearch(); return nil }},
		{id: "replace", title: "Edit: Replace in File...", desc: "Ctrl+H — Find and replace", action: func(m *Model) tea.Cmd { m.startReplace(); return nil }},
		{id: "undo", title: "Edit: Undo", desc: "Ctrl+Z — Undo change", action: func(m *Model) tea.Cmd { m.cur().buf.Undo(); return nil }},
		{id: "redo", title: "Edit: Redo", desc: "Ctrl+Y — Redo change", action: func(m *Model) tea.Cmd { m.cur().buf.Redo(); return nil }},
		{id: "git_commit", title: "Git: Commit Panel", desc: "Ctrl+G — Status, stage & commit", action: func(m *Model) tea.Cmd { m.openGitPanel(); return nil }},
		{id: "git_diff", title: "Git: Diff Selected File", desc: "D in panel — Side-by-side vs HEAD", action: func(m *Model) tea.Cmd { m.openDiffView(); return nil }},
		{id: "git_next", title: "Git: Next Hunk", desc: "Alt+] — Jump next hunk", action: func(m *Model) tea.Cmd { m.jumpHunk(1); return nil }},
		{id: "git_prev", title: "Git: Prev Hunk", desc: "Alt+[ — Jump prev hunk", action: func(m *Model) tea.Cmd { m.jumpHunk(-1); return nil }},
		{id: "split_v", title: "View: Split Vertical", desc: "Ctrl+\\ — Side-by-side split", action: func(m *Model) tea.Cmd { m.splitVert(); return nil }},
		{id: "split_h", title: "View: Split Horizontal", desc: "Ctrl+Alt+H — Stacked split", action: func(m *Model) tea.Cmd { m.splitHoriz(); return nil }},
		{id: "pane_focus", title: "View: Focus Other Pane", desc: "Ctrl+Alt+P — Switch pane", action: func(m *Model) tea.Cmd { m.focusOtherPane(); return nil }},
		{id: "pane_close", title: "View: Close Current Pane", desc: "Ctrl+Alt+W — Unsplit", action: func(m *Model) tea.Cmd { m.closePane(); return nil }},
		{id: "tree_toggle", title: "View: Toggle Project Tree", desc: "Ctrl+B — Sidebar tree", action: func(m *Model) tea.Cmd { m.toggleTree(); return nil }},
		{id: "terminal", title: "View: Toggle Terminal", desc: "Alt+T — Shell panel at the bottom", action: func(m *Model) tea.Cmd { return m.toggleTerminal() }},
		{id: "ai_chat", title: "AI: Toggle Chat Panel", desc: "Alt+A — Local Ollama chat on the right", action: func(m *Model) tea.Cmd { m.toggleChat(); return nil }},
		{id: "ai_inline", title: "AI: Inline Request", desc: "Alt+I — Rewrite selected text with AI", action: func(m *Model) tea.Cmd { m.startInlineRequest(); return nil }},
		{id: "ai_settings", title: "AI: Preferences...", desc: "Configure provider, model, URL, API key", action: func(m *Model) tea.Cmd { m.startAISettings(); return nil }},
		{id: "agent_tasks", title: "Agent: Task Panel", desc: "Alt+L — Background agent task list", action: func(m *Model) tea.Cmd { return m.openAgentPanel() }},
		{id: "agent_new", title: "Agent: New Task", desc: "Run a background refactoring task", action: func(m *Model) tea.Cmd { return m.startAgentTaskPrompt() }},
		{id: "settings", title: "Settings: Open Config", desc: "Open .dmed.conf for editing", action: func(m *Model) tea.Cmd { m.openConfigFile(); return nil }},
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

func (m *Model) handlePalette(msg tea.KeyPressMsg) tea.Cmd {
	hits := m.filterPalette()
	switch msg.String() {
	case "esc":
		m.paletteOpen = false
		m.paletteSel = 0
		m.paletteOffset = 0
		m.msg = ""
	case "enter":
		if len(hits) > 0 {
			if m.paletteSel >= len(hits) {
				m.paletteSel = 0
				m.paletteOffset = 0
			}
			sel := hits[m.paletteSel]
			m.paletteOpen = false
			m.paletteSel = 0
			m.paletteOffset = 0
			return sel.action(m)
		}
		m.paletteOpen = false
	case "up":
		if len(hits) > 0 {
			m.paletteSel = (m.paletteSel - 1 + len(hits)) % len(hits)
			m.clampPalette(hits)
		}
	case "down":
		if len(hits) > 0 {
			m.paletteSel = (m.paletteSel + 1) % len(hits)
			m.clampPalette(hits)
		}
	case "backspace":
		if len(m.paletteQ) > 0 {
			m.paletteQ = m.paletteQ[:len(m.paletteQ)-1]
			m.paletteSel = 0
			m.paletteOffset = 0
		}
	default:
		if len(msg.Text) > 0 {
			m.paletteQ = append(m.paletteQ, []rune(msg.Text)...)
			m.paletteSel = 0
			m.paletteOffset = 0
		}
	}
	return nil
}

// clampPalette keeps the palette viewport scrolled so paletteSel stays visible.
func (m *Model) clampPalette(hits []commandItem) {
	const paletteVisible = 8
	if len(hits) <= paletteVisible {
		m.paletteOffset = 0
		return
	}
	// Jump straight to the End when wrapping past the last item.
	if m.paletteSel > len(hits)-paletteVisible {
		m.paletteOffset = len(hits) - paletteVisible
		return
	}
	if m.paletteSel < m.paletteOffset {
		m.paletteOffset = m.paletteSel
	}
	if m.paletteSel >= m.paletteOffset+paletteVisible {
		m.paletteOffset = m.paletteSel - paletteVisible + 1
	}
}
