package editor

import (
	"strconv"
	"strings"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"dmed/internal/config"
)

// aiSettingsFields describes the wizard rows. The value is edited inline for
// text rows; the Provider row is a two-way choice cycled with ←/→.
var aiSettingsFields = []struct {
	name string
	kind string // "choice" | "text"
}{
	{name: "Provider", kind: "choice"},
	{name: "Model", kind: "text"},
	{name: "Ollama URL", kind: "text"},
	{name: "API Key", kind: "text"},
	{name: "Context Max", kind: "text"},
}

func (m *Model) startAISettings() {
	m.aiCfgOpen = true
	m.aiCfgField = 0
	m.aiCfgEdit = false
	m.aiCfgIn = nil
	m.msg = ""
}

func (m *Model) aiFieldValue(i int) string {
	switch i {
	case 0:
		return m.cfg.AI.Provider
	case 1:
		return m.cfg.AI.Model
	case 2:
		return m.cfg.AI.OllamaURL
	case 3:
		if m.cfg.AI.APIKey != "" {
			return "••••••••"
		}
		return ""
	case 4:
		return strconv.Itoa(m.cfg.AI.ContextMax)
	}
	return ""
}

func (m *Model) rawAIFieldValue(i int) string {
	if i == 3 {
		return m.cfg.AI.APIKey
	}
	return m.aiFieldValue(i)
}

func (m *Model) handleAISettings(msg tea.KeyPressMsg) tea.Cmd {
	if m.aiCfgEdit {
		switch msg.String() {
		case "esc":
			m.aiCfgEdit = false
			m.aiCfgIn = nil
		case "enter":
			m.commitAIField()
			m.aiCfgEdit = false
			m.aiCfgIn = nil
		case "backspace":
			if n := len(m.aiCfgIn); n > 0 {
				m.aiCfgIn = m.aiCfgIn[:n-1]
			}
		default:
			if len(msg.Text) > 0 {
				m.aiCfgIn = append(m.aiCfgIn, []rune(msg.Text)...)
			}
		}
		return nil
	}

	switch msg.String() {
	case "esc":
		m.aiCfgOpen = false
		m.aiCfgIn = nil
	case "j", "down":
		if m.aiCfgField < len(aiSettingsFields)-1 {
			m.aiCfgField++
		}
	case "k", "up":
		if m.aiCfgField > 0 {
			m.aiCfgField--
		}
	case "left":
		if m.aiCfgField == 0 {
			m.cycleAIProvider(-1)
		}
	case "right":
		if m.aiCfgField == 0 {
			m.cycleAIProvider(1)
		}
	case "enter":
		if m.aiCfgField == 0 {
			if m.aiCfgField < len(aiSettingsFields)-1 {
				m.aiCfgField++
			}
		} else {
			m.aiCfgEdit = true
			m.aiCfgIn = []rune(m.rawAIFieldValue(m.aiCfgField))
		}
	case "ctrl+s":
		m.saveAISettings()
	}
	return nil
}

func (m *Model) cycleAIProvider(d int) {
	if m.cfg.AI.Provider == "openai" {
		m.cfg.AI.Provider = "ollama"
	} else {
		m.cfg.AI.Provider = "openai"
	}
}

func (m *Model) commitAIField() {
	v := strings.TrimSpace(string(m.aiCfgIn))
	switch m.aiCfgField {
	case 1:
		m.cfg.AI.Model = v
	case 2:
		m.cfg.AI.OllamaURL = v
	case 3:
		m.cfg.AI.APIKey = v
	case 4:
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			m.cfg.AI.ContextMax = n
		}
	}
}

func (m *Model) aiConfigPath() string {
	if m.root != "" {
		return config.ProjectConfigPath(m.root)
	}
	return config.ConfigPath()
}

func (m *Model) saveAISettings() {
	path := m.aiConfigPath()
	if _, err := config.WriteAI(path, m.cfg.AI); err != nil {
		m.msg = "AI settings write failed: " + err.Error()
		return
	}
	// Reload so defaults/env overrides merge with the persisted values and the
	// live provider config reflects the change immediately.
	m.cfg = config.Load(m.root)
	m.msg = "AI settings saved"
}

func (m Model) aiSettingsPanel(h int) []string {
	rows := make([]string, 0, h)
	rows = append(rows, statusHiStyle.Render(" AI — settings ")+" "+hintStyle.Render("(↑/↓ move, Enter edit, ←/→ choice, Ctrl+S save, Esc close)"))
	for i, f := range aiSettingsFields {
		marker := " "
		if i == m.aiCfgField {
			marker = ">"
		}
		if i == 0 {
			v := m.cfg.AI.Provider
			rows = append(rows, " "+statusHiStyle.Render(marker)+" "+padTo(f.name, 12)+" "+statusStyle.Render(v)+"   "+hintStyle.Render("(←/→ choose)"))
			continue
		}
		rows = append(rows, " "+marker+" "+padTo(f.name, 12)+" "+statusStyle.Render(m.aiFieldValue(i)))
	}
	return rows
}

func (m Model) aiCfgEditLine() string {
	label := " " + aiSettingsFields[m.aiCfgField].name + ": "
	if m.aiCfgField == 3 {
		masked := string(m.aiCfgIn)
		if masked != "" {
			masked = strings.Repeat("•", len(masked))
		}
		return statusHiStyle.Render(label) + statusStyle.Render(masked) + cursorStyle.Render(" ")
	}
	line := statusHiStyle.Render(label) + statusStyle.Render(string(m.aiCfgIn)) + cursorStyle.Render(" ")
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

func (m Model) aiCfgBottom() string {
	line := statusHiStyle.Render(" AI settings ") + statusStyle.Render("(Ctrl+S save, Esc close)")
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}
