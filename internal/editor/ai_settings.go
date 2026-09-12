package editor

import (
	"context"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"dmed/internal/ai"
	"dmed/internal/config"
)

// aiSettingsFields describes the wizard rows. The value is edited inline for
// text rows; the Provider row is a choice cycled with ←/→ over the built-in
// presets (see config.AIPresets), and the Test row probes the provider.
var aiSettingsFields = []struct {
	name string
	kind string // "choice" | "text" | "action"
}{
	{name: "Provider", kind: "choice"},
	{name: "Model", kind: "text"},
	{name: "Base URL", kind: "text"},
	{name: "API Key", kind: "text"},
	{name: "Context Max", kind: "text"},
	{name: "Temperature", kind: "text"},
	{name: "Num Ctx", kind: "text"},
	{name: "Num Predict", kind: "text"},
	{name: "Tool Rounds", kind: "text"},
	{name: "Allow Run", kind: "choice"},
	{name: "Restrict Root", kind: "choice"},
	{name: "Test", kind: "action"},
}

// aiTestState tracks the background connection probe started from the Test row.
type aiTestState struct {
	running bool
	ok      bool
	status  string // one-line result shown on the Test row
}

// AITestResultMsg carries the outcome of a wizard connection test started by
// testAIConnection. Fields are plain values, so handling stays side-effect free.
type AITestResultMsg struct {
	OK     bool
	Status string
}

func (m *Model) startAISettings() {
	m.aiCfgOpen = true
	m.aiCfgField = 0
	m.aiCfgEdit = false
	m.aiCfgIn = nil
	m.aiCfgTest = aiTestState{}
	m.syncProviderKind()
	m.msg = ""
}

// syncProviderKind maps the stored provider label onto the matching preset's
// display name. Unknown labels (e.g. a hand-edited config with provider =
// ollama) fall back to the first preset so cycling and saving keep working.
func (m *Model) syncProviderKind() {
	m.cfg.AI.Provider = config.ResolvePreset(m.cfg.AI.Provider).Name
}

// cycleAIProvider moves to the previous/next built-in preset and applies its
// defaults: base URL always, model only when the provider exposes a stable
// suggestion and the user has not pinned one. URL is left untouched for Custom
// so users with a self-hosted endpoint keep their value.
func (m *Model) cycleAIProvider(d int) {
	presets := config.AIPresets()
	cur := config.ResolvePreset(m.cfg.AI.Provider)
	idx := 0
	for i, p := range presets {
		if p.Name == cur.Name {
			idx = i
			break
		}
	}
	idx = (idx + d + len(presets)) % len(presets)
	p := presets[idx]
	m.cfg.AI.Provider = p.Name
	if p.BaseURL != "" {
		m.cfg.AI.OllamaURL = p.BaseURL
	}
	if p.Model != "" && m.cfg.AI.Model == "" {
		m.cfg.AI.Model = p.Model
	}
	if p.Name != cur.Name { // switching providers invalidates a previous probe
		m.aiCfgTest = aiTestState{}
	}
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
		m.aiCfgField = (m.aiCfgField + 1) % len(aiSettingsFields)
	case "k", "up":
		m.aiCfgField = (m.aiCfgField - 1 + len(aiSettingsFields)) % len(aiSettingsFields)
	case "left":
		if m.aiCfgField == 0 {
			m.cycleAIProvider(-1)
		}
	case "right":
		if m.aiCfgField == 0 {
			m.cycleAIProvider(1)
		}
	case "t", "T":
		return m.testAIConnection()
	case "enter":
		if m.aiCfgField == 0 {
			m.cycleAIProvider(1)
		} else if m.aiCfgField == len(aiSettingsFields)-1 {
			return m.testAIConnection()
		} else {
			m.aiCfgEdit = true
			m.aiCfgIn = []rune(m.rawAIFieldValue(m.aiCfgField))
		}
	case "ctrl+s":
		m.saveAISettings()
	}
	return nil
}

// testAIConnection launches a background probe of the current provider
// settings. The result lands as AITestResultMsg; nothing is persisted here.
func (m *Model) testAIConnection() tea.Cmd {
	if m.aiCfgTest.running {
		return nil
	}
	prov := ai.NewProvider(ai.Config{
		Type:   ai.ProviderType(m.currentProviderKind()),
		URL:    m.cfg.AI.OllamaURL,
		Model:  m.cfg.AI.Model,
		APIKey: m.cfg.AI.APIKey,
	})
	m.aiCfgTest = aiTestState{running: true, status: "testing..."}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		models, err := prov.Models(ctx)
		if err != nil {
			return AITestResultMsg{OK: false, Status: strings.TrimSpace(err.Error())}
		}
		return AITestResultMsg{OK: true, Status: strconv.Itoa(len(models)) + " models"}
	}
}

// currentProviderKind returns the wire protocol for the provider label
// currently selected in the wizard ("ollama" | "openai").
func (m *Model) currentProviderKind() string {
	return config.ResolvePreset(m.cfg.AI.Provider).Kind
}

// handleAITestResult records the outcome of the connection probe and, on
// failure, replaces the terse transport error with a hint a beginner can act on.
func (m *Model) handleAITestResult(res AITestResultMsg) {
	status, ok := res.Status, res.OK
	if !ok {
		l := strings.ToLower(status)
		switch {
		case strings.Contains(l, "refused"):
			if m.currentProviderKind() == "ollama" {
				status = "refused — start ollama (or run: ollama serve)"
			} else {
				status = "refused — is the server running?"
			}
		case strings.Contains(l, "401") || strings.Contains(l, "unauthorized") || strings.Contains(l, "invalid"):
			status = "auth failed — check API Key"
		case strings.Contains(l, "no such host") || strings.Contains(l, "dial tcp") || strings.Contains(l, "timeout") || strings.Contains(l, "context deadline"):
			status = "unreachable — check Base URL"
		}
	}
	m.aiCfgTest = aiTestState{ok: ok, status: status}
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
	m.syncProviderKind()
	path := m.aiConfigPath()
	if _, err := config.WriteAI(path, m.cfg.AI); err != nil {
		m.msg = "AI settings write failed: " + err.Error()
		return
	}
	// Reload so defaults/env overrides merge with the persisted values and the
	// live provider config reflects the change immediately. The wizard's label
	// is preserved, so cycling inside an open wizard keeps pointing at the same
	// preset after the reload.
	was := m.cfg.AI.Provider
	m.cfg = config.Load(m.root)
	if m.cfg.AI.Provider != was {
		m.cfg.AI.Provider = was
	}
	m.msg = "AI settings saved"
}

func (m Model) aiSettingsPanel(h int) []string {
	rows := make([]string, 0, h)
	rows = append(rows, statusHiStyle.Render(m.t("ai.settings"))+" "+hintStyle.Render(m.t("ai.settings_hint")))
	for i, f := range aiSettingsFields {
		marker := " "
		if i == m.aiCfgField {
			marker = ">"
		}
		if i == 0 {
			rows = append(rows, " "+statusHiStyle.Render(marker)+" "+padTo(f.name, 12)+" "+statusStyle.Render(m.cfg.AI.Provider)+"   "+hintStyle.Render(m.t("ai.choice")))
			continue
		}
		if f.kind == "action" {
			rows = append(rows, " "+statusHiStyle.Render(marker)+" "+padTo(f.name, 12)+" "+m.testStatusLine())
			continue
		}
		rows = append(rows, " "+marker+" "+padTo(f.name, 12)+" "+statusStyle.Render(m.aiFieldValue(i)))
	}
	return rows
}

// testStatusLine renders the outcome of the last connection probe: green
// check with the model count, red cross with the human hint, or a neutral
// "not tested yet" placeholder.
func (m Model) testStatusLine() string {
	switch {
	case m.aiCfgTest.running:
		return hintStyle.Render("testing...")
	case m.aiCfgTest.ok:
		return okTestStyle.Render("✓ connected · " + m.aiCfgTest.status)
	case m.aiCfgTest.status != "":
		return errTestStyle.Render("✗ " + m.aiCfgTest.status)
	default:
		return hintStyle.Render(m.t("ai.test_hint"))
	}
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
	line := statusHiStyle.Render(m.t("ai.settings")) + statusStyle.Render(m.t("ai.settings_save"))
	fill := m.width - lipgloss.Width(line)
	if fill > 0 {
		line += statusStyle.Render(strings.Repeat(" ", fill))
	}
	return line
}
