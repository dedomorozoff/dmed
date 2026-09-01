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
		{id: "save", title: "cmd.save_t", desc: "cmd.save_d", action: func(m *Model) tea.Cmd { m.saveActive(); return nil }},
		{id: "save_as", title: "cmd.save_as_t", desc: "cmd.save_as_d", action: func(m *Model) tea.Cmd { m.startSavePrompt(); return nil }},
		{id: "close_tab", title: "cmd.close_tab_t", desc: "cmd.close_tab_d", action: func(m *Model) tea.Cmd { return m.closeTab() }},
		{id: "open", title: "cmd.open_t", desc: "cmd.open_d", action: func(m *Model) tea.Cmd { m.startPrompt(); return nil }},
		{id: "new_file", title: "cmd.new_file_t", desc: "cmd.new_file_d", action: func(m *Model) tea.Cmd { m.startNewFilePrompt(); return nil }},
		{id: "new_folder", title: "cmd.new_folder_t", desc: "cmd.new_folder_d", action: func(m *Model) tea.Cmd { m.startNewFolderPrompt(); return nil }},
		{id: "finder", title: "cmd.finder_t", desc: "cmd.finder_d", action: func(m *Model) tea.Cmd { m.startFinder(); return nil }},
		{id: "search", title: "cmd.search_t", desc: "cmd.search_d", action: func(m *Model) tea.Cmd { m.startSearch(); return nil }},
		{id: "replace", title: "cmd.replace_t", desc: "cmd.replace_d", action: func(m *Model) tea.Cmd { m.startReplace(); return nil }},
		{id: "undo", title: "cmd.undo_t", desc: "cmd.undo_d", action: func(m *Model) tea.Cmd { m.cur().buf.Undo(); return nil }},
		{id: "redo", title: "cmd.redo_t", desc: "cmd.redo_d", action: func(m *Model) tea.Cmd { m.cur().buf.Redo(); return nil }},
		{id: "git_commit", title: "cmd.git_commit_t", desc: "cmd.git_commit_d", action: func(m *Model) tea.Cmd { m.openGitPanel(); return nil }},
		{id: "git_diff", title: "cmd.git_diff_t", desc: "cmd.git_diff_d", action: func(m *Model) tea.Cmd { m.openDiffView(); return nil }},
		{id: "git_next", title: "cmd.git_next_t", desc: "cmd.git_next_d", action: func(m *Model) tea.Cmd { m.jumpHunk(1); return nil }},
		{id: "git_prev", title: "cmd.git_prev_t", desc: "cmd.git_prev_d", action: func(m *Model) tea.Cmd { m.jumpHunk(-1); return nil }},
		{id: "split_v", title: "cmd.split_v_t", desc: "cmd.split_v_d", action: func(m *Model) tea.Cmd { m.splitVert(); return nil }},
		{id: "split_h", title: "cmd.split_h_t", desc: "cmd.split_h_d", action: func(m *Model) tea.Cmd { m.splitHoriz(); return nil }},
		{id: "pane_focus", title: "cmd.pane_focus_t", desc: "cmd.pane_focus_d", action: func(m *Model) tea.Cmd { m.focusOtherPane(); return nil }},
		{id: "pane_close", title: "cmd.pane_close_t", desc: "cmd.pane_close_d", action: func(m *Model) tea.Cmd { m.closePane(); return nil }},
		{id: "tree_toggle", title: "cmd.tree_toggle_t", desc: "cmd.tree_toggle_d", action: func(m *Model) tea.Cmd { m.toggleTree(); return nil }},
		{id: "terminal", title: "cmd.terminal_t", desc: "cmd.terminal_d", action: func(m *Model) tea.Cmd { return m.toggleTerminal() }},
		{id: "ai_chat", title: "cmd.ai_chat_t", desc: "cmd.ai_chat_d", action: func(m *Model) tea.Cmd { m.toggleChat(); return nil }},
		{id: "ai_inline", title: "cmd.ai_inline_t", desc: "cmd.ai_inline_d", action: func(m *Model) tea.Cmd { m.startInlineRequest(); return nil }},
		{id: "ai_settings", title: "cmd.ai_settings_t", desc: "cmd.ai_settings_d", action: func(m *Model) tea.Cmd { m.startAISettings(); return nil }},
		{id: "agent_tasks", title: "cmd.agent_tasks_t", desc: "cmd.agent_tasks_d", action: func(m *Model) tea.Cmd { return m.openAgentPanel() }},
		{id: "agent_new", title: "cmd.agent_new_t", desc: "cmd.agent_new_d", action: func(m *Model) tea.Cmd { return m.startAgentTaskPrompt() }},
		{id: "settings", title: "cmd.settings_t", desc: "cmd.settings_d", action: func(m *Model) tea.Cmd { m.openConfigFile(); return nil }},
		{id: "help", title: "cmd.help_t", desc: "cmd.help_d", action: func(m *Model) tea.Cmd { m.helpOpen = true; return nil }},
		{id: "quit", title: "cmd.quit_t", desc: "cmd.quit_d", action: func(m *Model) tea.Cmd { return tea.Quit }},
	}
}

// cmdTitle returns the localized command title.
func (m Model) cmdTitle(c commandItem) string { return m.t(c.title) }

// cmdDesc returns the localized command description.
func (m Model) cmdDesc(c commandItem) string { return m.t(c.desc) }

func (m *Model) filterPalette() []commandItem {
	all := m.getPaletteCommands()
	q := strings.ToLower(string(m.paletteQ))
	if q == "" {
		return all
	}
	var res []commandItem
	for _, c := range all {
		if strings.Contains(strings.ToLower(m.cmdTitle(c)), q) || strings.Contains(strings.ToLower(m.cmdDesc(c)), q) {
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
