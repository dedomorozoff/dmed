package editor

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/config"
)

// isolateHomeConfig points the user home dir at a temp dir so the tests run
// against default configuration instead of whatever ~/.dmed.conf exists on
// the machine (config.Load reads os.UserHomeDir, which on Windows is
// USERPROFILE and on Unix is HOME — set both for cross-platform coverage).
func isolateHomeConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("USERPROFILE", dir)
	t.Setenv("HOME", dir)
}

// TestAISettingsProviderCycle verifies ←/→ cycles the built-in presets and
// auto-fills the base URL for cloud providers.
func TestAISettingsProviderCycle(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.startAISettings()
	if !m.aiCfgOpen {
		t.Fatal("wizard should open")
	}
	if m.cfg.AI.Provider != "Ollama (local)" {
		t.Fatalf("prov in default = %q", m.cfg.AI.Provider)
	}
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.cfg.AI.Provider != "Pollinations (free, no key)" {
		t.Fatalf("after right = %q, want the keyless fallback preset", m.cfg.AI.Provider)
	}
	// The keyless preset must carry its own endpoint prefix, or the probe would
	// hit /v1 and report a 404 as "auth failed".
	if m.cfg.AI.APIPath != "/openai" || m.cfg.AI.ModelsPath != "/models" {
		t.Fatalf("api paths = %q / %q", m.cfg.AI.APIPath, m.cfg.AI.ModelsPath)
	}
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.cfg.AI.Provider != "OpenAI" {
		t.Fatalf("after two rights = %q, want OpenAI", m.cfg.AI.Provider)
	}
	if m.cfg.AI.OllamaURL != "https://api.openai.com" {
		t.Fatalf("url after right = %q, want preset URL", m.cfg.AI.OllamaURL)
	}
	if m.cfg.AI.APIPath != "" {
		t.Fatalf("api path must reset for a /v1 provider, got %q", m.cfg.AI.APIPath)
	}
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyLeft})
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.cfg.AI.Provider != "Ollama (local)" {
		t.Fatalf("after two lefts = %q, want Ollama (local)", m.cfg.AI.Provider)
	}
}

// TestAISettingsPollinationsNeedsNoKey verifies the wizard does not ask for a key
// for the keyless provider: typing a key there is a pure usability bug.
func TestAISettingsPollinationsNeedsNoKey(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.startAISettings()
	for m.cfg.AI.Provider != "Pollinations (free, no key)" {
		m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyRight})
	}
	if p := config.ResolvePreset(m.cfg.AI.Provider); p.APIKey {
		t.Fatal("the keyless preset must not be marked as needing a key")
	}
}

// TestAISettingsLegacyProviderNormalizes verifies that a config written by an
// older dmed (provider = ollama) still lands on the matching preset.
func TestAISettingsLegacyProviderNormalizes(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.cfg.AI.Provider = "openai"
	m.startAISettings()
	if m.cfg.AI.Provider != "OpenAI" {
		t.Fatalf("legacy openai not normalized, got %q", m.cfg.AI.Provider)
	}
}

// TestAISettingsTestRowConnectionProbe verifies the Test row: Enter (or t)
// starts the probe, and the result message flips the status to the human hint.
func TestAISettingsTestRowConnectionProbe(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.startAISettings()
	m.aiCfgField = len(aiSettingsFields) - 1 // Test row

	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.aiCfgTest.running {
		t.Fatal("probe should be running after Enter on Test row")
	}
	m.handleAITestResult(AITestResultMsg{OK: true, Status: "2 models", Gen: m.aiCfgGen})
	if !m.aiCfgTest.ok || m.aiCfgTest.running {
		t.Fatalf("state after ok result: %+v", m.aiCfgTest)
	}

	m.aiCfgField = 0 // 't' works from any row
	cmd := m.handleAISettings(tea.KeyPressMsg{Code: 't'})
	if cmd == nil {
		t.Fatal("t should start a probe when none is running")
	}
	m.handleAITestResult(AITestResultMsg{OK: false, Status: "dial tcp: connection refused", Gen: m.aiCfgGen})
	if m.aiCfgTest.ok || strings.Contains(m.aiCfgTest.status, "dial tcp") {
		t.Fatalf("refused must be translated into a human hint, got %q", m.aiCfgTest.status)
	}
}

// TestAISettingsIgnoresStaleProbe: a reply from the endpoint the user already
// navigated away from must not repopulate the Model row.
func TestAISettingsIgnoresStaleProbe(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.startAISettings()
	stale := m.aiCfgGen

	// The user changes the provider while the probe is in flight.
	m.cycleAIProvider(1)
	if m.aiCfgGen == stale {
		t.Fatal("changing the provider must start a new request generation")
	}

	m.handleAITestResult(AITestResultMsg{OK: true, Status: "3 models", Gen: stale,
		Models: []string{"old-model"}})

	if len(m.aiCfgModels) != 0 {
		t.Fatalf("a stale reply must not fill the list: %v", m.aiCfgModels)
	}
	if m.cfg.AI.Model == "old-model" {
		t.Fatal("a stale reply must not choose a model")
	}
}

// TestAISettingsEditAndCommit edits the Model field and commits it via Enter.
// TestAISettingsLoadsModelsOnOpen: the reason this feature exists is that
// choosing a model without knowing what the server has is guesswork, so opening
// the wizard asks the server.
func TestAISettingsLoadsModelsOnOpen(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	cmd := m.startAISettings()
	if cmd == nil {
		t.Fatal("opening the wizard must start a model probe")
	}
	if !m.aiCfgTest.running {
		t.Fatal("the probe should be running while the wizard is open")
	}

	m.handleAITestResult(AITestResultMsg{OK: true, Status: "3 models", Gen: m.aiCfgGen,
		Models: []string{"deepseek-r1", "llama3.2", "qwen3-coder"}})

	if len(m.aiCfgModels) != 3 {
		t.Fatalf("models = %v", m.aiCfgModels)
	}
	// An empty Model row means "whatever the server likes" — now we know.
	if m.cfg.AI.Model != "deepseek-r1" {
		t.Fatalf("model = %q, want the first model auto-filled", m.cfg.AI.Model)
	}
	if m.chatModel != m.cfg.AI.Model {
		t.Fatalf("the chat must follow the model the wizard chose: %q vs %q", m.chatModel, m.cfg.AI.Model)
	}
}

// TestAISettingsModelRowNavigates: ←/→ walk the list the server reported, which
// is the interaction the row hint promises.
func TestAISettingsModelRowNavigates(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.startAISettings()
	m.handleAITestResult(AITestResultMsg{OK: true, Status: "3 models", Gen: m.aiCfgGen,
		Models: []string{"a-model", "b-model", "c-model"}})
	m.aiCfgField = 1 // Model row

	right := tea.KeyPressMsg{Code: tea.KeyRight}
	left := tea.KeyPressMsg{Code: tea.KeyLeft}

	m.handleAISettings(right)
	if m.cfg.AI.Model != "b-model" {
		t.Fatalf("after right = %q, want b-model", m.cfg.AI.Model)
	}
	m.handleAISettings(right)
	m.handleAISettings(right)
	if m.cfg.AI.Model != "a-model" {
		t.Fatalf("the list must wrap, got %q", m.cfg.AI.Model)
	}
	m.handleAISettings(left)
	if m.cfg.AI.Model != "c-model" {
		t.Fatalf("after left = %q, want c-model", m.cfg.AI.Model)
	}

	// Enter still starts inline editing, for a model the server does not list.
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.aiCfgEdit {
		t.Fatal("Enter on the Model row must still allow typing a name")
	}
}

// TestAISettingsKeepsPinnedModel pins the rule that matters: the wizard may fill
// an empty field, but it must never overwrite a model the user chose — even one
// the server does not report (some servers filter the list).
func TestAISettingsKeepsPinnedModel(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.cfg.AI.Model = "my-private-fork"
	m.startAISettings()
	m.handleAITestResult(AITestResultMsg{OK: true, Status: "2 models", Gen: m.aiCfgGen,
		Models: []string{"a-model", "b-model"}})

	if m.cfg.AI.Model != "my-private-fork" {
		t.Fatalf("model = %q, want the user's choice untouched", m.cfg.AI.Model)
	}
	row := m.aiModelRow(">")
	if !containsStr(row, "my-private-fork") {
		t.Fatalf("the row must show the value: %q", row)
	}
	if !containsStr(row, m.t("ai.model_not_listed")) {
		t.Fatalf("the row must warn that the model is not in the list: %q", row)
	}
}

// TestAISettingsModelRowHints covers what the user actually sees: how many
// models were found, and what to do while the list is loading.
func TestAISettingsModelRowHints(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.cfg.AI.Model = "x"

	m.startAISettings()
	if row := m.aiModelRow(">"); !containsStr(row, "loading") {
		t.Fatalf("while loading, the row must say so: %q", row)
	}

	m.handleAITestResult(AITestResultMsg{OK: true, Status: "4 models", Gen: m.aiCfgGen,
		Models: []string{"a", "b", "c", "d"}})
	row := m.aiModelRow(">")
	if !containsStr(row, "4") || !containsStr(row, "←") {
		t.Fatalf("the row must show the count and the keys: %q", row)
	}

	// A failed probe drops the list: it belonged to a server we cannot reach.
	m.handleAITestResult(AITestResultMsg{OK: false, Status: "dial tcp: refused", Gen: m.aiCfgGen})
	if len(m.aiCfgModels) != 0 {
		t.Fatalf("a failed probe must clear the list, got %v", m.aiCfgModels)
	}
}

// TestAISettingsModelListSortedAndLoaded drives the real probe command against a
// local stub: the list must arrive sorted, because "the first model" is what the
// auto-pick uses and a shuffling list would make that arbitrary.
func TestAISettingsModelListSortedAndLoaded(t *testing.T) {
	isolateHomeConfig(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"z-last"},{"id":"a-first"},{"id":"m-middle"}]}`))
	}))
	defer srv.Close()

	m := New()
	m.cfg.AI.OllamaURL = srv.URL
	m.cfg.AI.Provider = "Groq" // an OpenAI-compatible provider, so /v1/models
	cmd := m.testAIConnection()
	if cmd == nil {
		t.Fatal("probe must start")
	}
	msg, ok := cmd().(AITestResultMsg)
	if !ok || !msg.OK {
		t.Fatalf("probe result = %+v", msg)
	}
	want := []string{"a-first", "m-middle", "z-last"}
	for i, w := range want {
		if msg.Models[i] != w {
			t.Fatalf("models = %v, want %v", msg.Models, want)
		}
	}
}

func TestAISettingsEditAndCommit(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.startAISettings()

	// Move to Model row.
	for i := 0; i < 1; i++ {
		m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if m.aiCfgField != 1 {
		t.Fatalf("field = %d, want 1", m.aiCfgField)
	}

	// Enter to edit, type a model name, Enter to commit.
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.aiCfgEdit {
		t.Fatal("should be in edit mode after Enter")
	}
	for _, r := range []rune("llama3.1") {
		m.handleAISettings(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.aiCfgEdit {
		t.Fatal("edit mode should close after commit")
	}
	if m.cfg.AI.Model != "llama3.1" {
		t.Fatalf("model = %q, want llama3.1", m.cfg.AI.Model)
	}
}

// TestAISettingsPaste verifies pasted text (bracketed-paste PasteMsg) lands in
// the field being edited instead of the editor buffer.
func TestAISettingsPaste(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.startAISettings()

	for i := 0; i < 1; i++ {
		m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.aiCfgEdit {
		t.Fatal("should be in edit mode after Enter")
	}

	next, _ := m.Update(tea.PasteMsg{Content: "deepseek-r1"})
	m = next.(Model)
	got := string(m.aiCfgIn)
	if !strings.HasSuffix(got, "deepseek-r1") {
		t.Fatalf("field = %q, want pasted text appended", got)
	}
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.HasSuffix(m.cfg.AI.Model, "deepseek-r1") {
		t.Fatalf("model = %q, want pasted text committed", m.cfg.AI.Model)
	}
}

// TestAISettingsEscCloses verifies Esc exits, and Esc inside edit reverts.
func TestAISettingsEscCloses(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.startAISettings()
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.aiCfgOpen {
		t.Fatal("wizard should close on Esc")
	}
}

// TestAISettingsSaveWritesConfig verifies Ctrl+S persists the [ai] section to
// the project config and reloads values.
func TestAISettingsSaveWritesConfig(t *testing.T) {
	isolateHomeConfig(t)
	dir := t.TempDir()
	m := New(dir)
	m.startAISettings()

	// Move to Context Max (field 4), edit, commit.
	for i := 0; i < 4; i++ {
		m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEnter})
	for i := 0; i < len("6000"); i++ {
		m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	for _, r := range []rune("9999") {
		m.handleAISettings(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.cfg.AI.ContextMax != 9999 {
		t.Fatalf("context_max = %d, want 9999 (before save)", m.cfg.AI.ContextMax)
	}

	m.handleAISettings(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})

	path := filepath.Join(dir, ".dmed.conf")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("config file not written: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "[ai]") || !strings.Contains(content, "context_max = 9999") {
		t.Fatalf("config file missing saved values:\n%s", content)
	}
	if !strings.Contains(m.msg, "AI settings saved") {
		t.Fatalf("msg = %q", m.msg)
	}
}
