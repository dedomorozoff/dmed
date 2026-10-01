package editor

import (
	"context"
	"fmt"
	"sort"
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
// Models is filled on success: the provider's own list is what turns "which
// model do I pick?" into a question with a visible answer.
type AITestResultMsg struct {
	OK     bool
	Status string
	Models []string
	// Gen identifies the request this reply belongs to. The wizard can have a
	// probe in flight while the user changes provider or URL, and a late reply
	// from the previous endpoint must not repopulate the new one.
	Gen int
}

func (m *Model) startAISettings() tea.Cmd {
	m.aiCfgOpen = true
	m.aiCfgField = 0
	m.aiCfgEdit = false
	m.aiCfgIn = nil
	m.aiCfgTest = aiTestState{}
	// The list belongs to the endpoint, so a reopened wizard starts empty and
	// asks again rather than showing one that belongs to another server.
	m.aiCfgModels = nil
	m.syncProviderKind()
	m.msg = ""
	// Load straight away: the commonest question about AI settings is "which
	// model can I even choose here?", and the answer is one request away.
	return m.testAIConnection()
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
//
// It reloads the model list, because that is the whole point of the wizard: a
// provider change without the list means the Model row is a guess.
func (m *Model) cycleAIProvider(d int) tea.Cmd {
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
	// Choosing a provider is a decision, so the session stops being a fallback
	// (and stops claiming the traffic is a fallback) even when the chosen one
	// happens to be the keyless provider.
	m.aiFallback = false
	m.chatNotice = ""
	if p.BaseURL != "" {
		m.cfg.AI.OllamaURL = p.BaseURL
	}
	// The endpoint prefix travels with the preset: Pollinations serves the
	// OpenAI protocol under /openai, so keeping the previous prefix would make
	// the new provider answer 404s.
	m.cfg.AI.APIPath = p.APIPath
	m.cfg.AI.ModelsPath = p.ModelsPath
	if p.Model != "" && m.cfg.AI.Model == "" {
		m.cfg.AI.Model = p.Model
	}
	if p.Name != cur.Name { // switching providers invalidates a previous probe
		m.aiCfgTest = aiTestState{}
		// Another server means another model list, so ask it instead of showing
		// the previous provider's models.
		m.aiCfgModels = nil
		// A model that only exists on the old server would fail on the new one.
		if config.ResolvePreset(cur.Name).Model == "" && m.cfg.AI.Model == p.Model {
			m.cfg.AI.Model = ""
		}
		return m.testAIConnection()
	}
	return nil
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
			return strings.Repeat(m.g.mask, 8)
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
		switch m.aiCfgField {
		case 0:
			m.cycleAIProvider(-1)
		case 1:
			m.cycleAIModel(-1)
		}
	case "right":
		switch m.aiCfgField {
		case 0:
			m.cycleAIProvider(1)
		case 1:
			m.cycleAIModel(1)
		}
	case "t", "T":
		return m.testAIConnection()
	case "enter":
		if m.aiCfgField == 0 {
			return m.cycleAIProvider(1)
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
// settings. The result lands as AITestResultMsg and carries the provider's model
// list, so one request both checks the connection and fills the Model row.
// Nothing is persisted here.
func (m *Model) testAIConnection() tea.Cmd {
	if m.aiCfgTest.running {
		return nil
	}
	kind := ai.ProviderType(m.currentProviderKind())
	url, key := m.cfg.AI.OllamaURL, m.cfg.AI.APIKey
	apiPath, modelsPath := m.cfg.AI.APIPath, m.cfg.AI.ModelsPath
	m.aiCfgGen++
	gen := m.aiCfgGen
	m.aiCfgTest = aiTestState{running: true, status: "testing..."}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		prov := ai.NewProvider(ai.Config{
			Type: kind, URL: url, Model: m.cfg.AI.Model, APIKey: key,
			APIPath: apiPath, ModelsPath: modelsPath,
		})
		models, err := prov.Models(ctx)
		if err != nil {
			return AITestResultMsg{OK: false, Status: strings.TrimSpace(err.Error()), Gen: gen}
		}
		sort.Strings(models) // stable order so the list does not shuffle
		return AITestResultMsg{OK: true, Status: strconv.Itoa(len(models)) + " models", Models: models, Gen: gen}
	}
}

// handleAITestResult records the outcome of the connection probe and, on
// failure, replaces the terse transport error with a hint a beginner can act on.
// On success it stores the model list and fills the Model row when it is empty.
func (m *Model) handleAITestResult(res AITestResultMsg) {
	// A reply from a superseded request must not overwrite the current state:
	// the user may have changed provider or URL while the probe was in flight.
	if res.Gen != m.aiCfgGen {
		return
	}
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
		m.aiCfgTest = aiTestState{ok: ok, status: status}
		// The list we had belonged to a server we can no longer reach.
		m.aiCfgModels = nil
		return
	}
	m.aiCfgTest = aiTestState{ok: true, status: status}
	m.aiCfgModels = res.Models
	if len(res.Models) == 0 {
		return
	}
	// An empty Model field means "whatever the server likes"; the wizard can
	// fill it in now that the answer is known. A model the user already pinned
	// is left alone even when the server does not list it — some servers filter.
	if m.cfg.AI.Model == "" {
		m.applyAIModel(res.Models[0])
	}
}

// applyAIModel sets the model and tears down the cached provider, so the chat,
// ghost text and agents use it from the next request on — before Ctrl+S, which
// is only about persistence.
func (m *Model) applyAIModel(model string) {
	if model == "" || m.cfg.AI.Model == model {
		return
	}
	m.cfg.AI.Model = model
	m.chatModel = model
	m.ai = nil
	m.aiKey = ""
}

// cycleAIModel moves the selection through the loaded list. It reports whether
// anything moved, so the caller knows whether to refresh the provider.
func (m *Model) cycleAIModel(d int) bool {
	if len(m.aiCfgModels) == 0 {
		return false
	}
	idx := -1
	for i, name := range m.aiCfgModels {
		if name == m.cfg.AI.Model {
			idx = i
			break
		}
	}
	// No current match: step into the list from the nearest end.
	next := 0
	switch {
	case idx < 0 && d < 0:
		next = len(m.aiCfgModels) - 1
	case idx >= 0:
		next = (idx + d + len(m.aiCfgModels)) % len(m.aiCfgModels)
	}
	m.applyAIModel(m.aiCfgModels[next])
	return true
}

// currentProviderKind returns the wire protocol for the provider label
// currently selected in the wizard ("ollama" | "openai").
func (m *Model) currentProviderKind() string {
	return config.ResolvePreset(m.cfg.AI.Provider).Kind
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
	// Invalidate the cached provider so the next aiProvider() call rebuilds it
	// from the settings just saved — otherwise the wizard would appear to save
	// while chat/ghost/agent kept talking to the old endpoint and key.
	m.ai = nil
	m.aiKey = ""
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
		if i == 1 {
			rows = append(rows, m.aiModelRow(marker))
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

// aiModelRow renders the Model row: the value plus what the provider actually
// offers. Without the count the row is an opaque text field, which is the whole
// complaint this row exists to answer.
func (m Model) aiModelRow(marker string) string {
	value := m.cfg.AI.Model
	hint := m.t("ai.model_hint")
	switch {
	case len(m.aiCfgModels) > 0:
		hint = fmt.Sprintf(m.t("ai.models_found"), len(m.aiCfgModels))
		if value != "" && !containsString(m.aiCfgModels, value) {
			// Keep the pinned value but say it is not what this server has.
			hint += "  " + m.t("ai.model_not_listed")
		}
	case m.aiCfgTest.running:
		hint = "loading..."
	case value == "":
		hint = m.t("ai.model_load_hint")
	}
	return " " + statusHiStyle.Render(marker) + " " + padTo(aiSettingsFields[1].name, 12) + " " +
		statusStyle.Render(value) + "  " + hintStyle.Render(hint)
}

// containsString is a tiny helper kept local so this file does not grow an
// import for one call.
func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// testStatusLine renders the outcome of the last connection probe: green
// check with the model count, red cross with the human hint, or a neutral
// "not tested yet" placeholder.
func (m Model) testStatusLine() string {
	switch {
	case m.aiCfgTest.running:
		return hintStyle.Render("testing...")
	case m.aiCfgTest.ok:
		return okTestStyle.Render(m.g.check + " connected " + m.g.dotSep + " " + m.aiCfgTest.status)
	case m.aiCfgTest.status != "":
		return errTestStyle.Render(m.g.cross + " " + m.aiCfgTest.status)
	default:
		return hintStyle.Render(m.t("ai.test_hint"))
	}
}

func (m Model) aiCfgEditLine() string {
	label := " " + aiSettingsFields[m.aiCfgField].name + ": "
	if m.aiCfgField == 3 {
		masked := string(m.aiCfgIn)
		if masked != "" {
			masked = strings.Repeat(m.g.mask, len(masked))
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
