package editor

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"dmed/internal/config"
)

// dapSettingsFields describes the DAP configuration wizard rows, mirroring the
// [debug] INI section. Choice rows are cycled with ←/→; the Adapter Mode and
// Launch Request rows are yes/no choices; every other row is inline text.
var dapSettingsFields = []struct {
	name string
	kind string // "choice" | "bool" | "text"
}{
	{name: "Adapter Cmd", kind: "text"},
	{name: "Adapter Mode", kind: "choice"},
	{name: "Adapter Args", kind: "text"},
	{name: "Launch Type", kind: "text"},
	{name: "Launch Request", kind: "choice"},
	{name: "Mode", kind: "text"},
	{name: "Program", kind: "text"},
	{name: "Args", kind: "text"},
	{name: "Stop On Entry", kind: "bool"},
	{name: "Launch JSON", kind: "text"},
}

// dapAdapterModes are the two supported adapter transports, in cycle order.
var dapAdapterModes = []string{"reverse", "stdio"}

// dapLaunchRequests are the two supported DAP request kinds, in cycle order.
var dapLaunchRequests = []string{"launch", "attach"}

const dapYesNoPos = "yes/no"

func (m *Model) startDAPCfg() {
	m.dapCfgOpen = true
	m.dapCfgField = 0
	m.dapCfgEdit = false
	m.dapCfgIn = nil
	m.msg = ""
}

func (m *Model) dapFieldValue(i int) string {
	switch i {
	case 0:
		return m.cfg.Debug.AdapterCmd
	case 1:
		return m.cfg.Debug.AdapterMode
	case 2:
		return m.cfg.Debug.AdapterArgs
	case 3:
		return m.cfg.Debug.LaunchType
	case 4:
		return m.cfg.Debug.LaunchRequest
	case 5:
		return m.cfg.Debug.Mode
	case 6:
		return m.cfg.Debug.Program
	case 7:
		return m.cfg.Debug.Args
	case 8:
		if m.cfg.Debug.StopOnEntry {
			return "yes"
		}
		return "no"
	case 9:
		return m.cfg.Debug.LaunchJSON
	}
	return ""
}

func (m *Model) cycleDAPChoice(i, d int) {
	if dapSettingsFields[i].kind == "choice" {
		if dapSettingsFields[i].name == "Adapter Mode" {
			m.cfg.Debug.AdapterMode = cycleString(dapAdapterModes, m.cfg.Debug.AdapterMode, d)
		} else if dapSettingsFields[i].name == "Launch Request" {
			m.cfg.Debug.LaunchRequest = cycleString(dapLaunchRequests, m.cfg.Debug.LaunchRequest, d)
		}
		return
	}
	if dapSettingsFields[i].kind == "bool" {
		m.cfg.Debug.StopOnEntry = !m.cfg.Debug.StopOnEntry
	}
}

func (m *Model) commitDAPField() {
	v := strings.TrimSpace(string(m.dapCfgIn))
	switch m.dapCfgField {
	case 0:
		m.cfg.Debug.AdapterCmd = v
	case 2:
		m.cfg.Debug.AdapterArgs = v
	case 3:
		m.cfg.Debug.LaunchType = v
	case 5:
		m.cfg.Debug.Mode = v
	case 6:
		m.cfg.Debug.Program = v
	case 7:
		m.cfg.Debug.Args = v
	case 9:
		m.cfg.Debug.LaunchJSON = v
	}
}

// handleDAPCfg routes keys while the DAP settings wizard is open.
func (m *Model) handleDAPCfg(msg tea.KeyPressMsg) tea.Cmd {
	if m.dapCfgEdit {
		switch msg.String() {
		case "esc":
			m.dapCfgEdit = false
			m.dapCfgIn = nil
		case "enter":
			m.commitDAPField()
			m.dapCfgEdit = false
			m.dapCfgIn = nil
		case "backspace":
			if n := len(m.dapCfgIn); n > 0 {
				m.dapCfgIn = m.dapCfgIn[:n-1]
			}
		default:
			if len(msg.Text) > 0 {
				m.dapCfgIn = append(m.dapCfgIn, []rune(msg.Text)...)
			}
		}
		return nil
	}

	switch msg.String() {
	case "esc":
		m.dapCfgOpen = false
		m.dapCfgIn = nil
	case "j", "down":
		m.dapCfgField = (m.dapCfgField + 1) % len(dapSettingsFields)
	case "k", "up":
		m.dapCfgField = (m.dapCfgField - 1 + len(dapSettingsFields)) % len(dapSettingsFields)
	case "left":
		m.cycleDAPChoice(m.dapCfgField, -1)
	case "right":
		m.cycleDAPChoice(m.dapCfgField, 1)
	case "enter":
		switch dapSettingsFields[m.dapCfgField].kind {
		case "choice", "bool":
			m.cycleDAPChoice(m.dapCfgField, 1)
		default:
			m.dapCfgEdit = true
			m.dapCfgIn = []rune(m.dapFieldValue(m.dapCfgField))
		}
	case "ctrl+s":
		m.saveDAPCfg()
	}
	return nil
}

func (m *Model) saveDAPCfg() {
	path := m.aiConfigPath() // shared: project config if present, else global
	if _, err := config.WriteDebug(path, m.cfg.Debug); err != nil {
		m.msg = "debug settings write failed: " + err.Error()
		return
	}
	m.cfg = config.Load(m.root)
	m.msg = m.t("msg.dap_saved")
}

func (m Model) dapCfgPanel(h int) []string {
	rows := make([]string, 0, h)
	rows = append(rows, statusHiStyle.Render(m.t("dap.settings"))+" "+hintStyle.Render(m.t("dap.settings_hint")))
	for i, f := range dapSettingsFields {
		marker := " "
		if i == m.dapCfgField {
			marker = ">"
		}
		kind := f.kind
		hint := ""
		switch kind {
		case "choice":
			hint = hintStyle.Render("  ←/→")
		case "bool":
			hint = hintStyle.Render("  ←/→")
		}
		rows = append(rows, " "+marker+" "+padTo(f.name, 16)+" "+statusStyle.Render(m.dapFieldValue(i))+hint)
	}
	return rows
}

func (m Model) dapCfgEditLine() string {
	if m.dapCfgField == 8 {
		// Bool rows are cycled, never edited inline.
		return m.dapCfgBottom()
	}
	label := " " + dapSettingsFields[m.dapCfgField].name + ": "
	line := statusHiStyle.Render(label) + statusStyle.Render(string(m.dapCfgIn)) + cursorStyle.Render(" ")
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

func (m Model) dapCfgBottom() string {
	line := statusHiStyle.Render(m.t("dap.settings")) + statusStyle.Render(m.t("dap.settings_save"))
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}

// cycleString moves cur one step within list (wrapping), defaulting to the
// first entry when cur is empty or unknown.
func cycleString(list []string, cur string, d int) string {
	if len(list) == 0 {
		return cur
	}
	idx := 0
	for i, s := range list {
		if s == cur {
			idx = i
			break
		}
	}
	idx = (idx + d + len(list)) % len(list)
	return list[idx]
}
