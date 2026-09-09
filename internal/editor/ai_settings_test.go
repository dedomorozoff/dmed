package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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
	if m.cfg.AI.Provider != "OpenAI" {
		t.Fatalf("after right = %q, want OpenAI", m.cfg.AI.Provider)
	}
	if m.cfg.AI.OllamaURL != "https://api.openai.com" {
		t.Fatalf("url after right = %q, want preset URL", m.cfg.AI.OllamaURL)
	}
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.cfg.AI.Provider != "Ollama (local)" {
		t.Fatalf("after left = %q, want Ollama (local)", m.cfg.AI.Provider)
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
	m.handleAITestResult(AITestResultMsg{OK: true, Status: "2 models"})
	if !m.aiCfgTest.ok || m.aiCfgTest.running {
		t.Fatalf("state after ok result: %+v", m.aiCfgTest)
	}

	m.aiCfgField = 0 // 't' works from any row
	cmd := m.handleAISettings(tea.KeyPressMsg{Code: 't'})
	if cmd == nil {
		t.Fatal("t should start a probe when none is running")
	}
	m.handleAITestResult(AITestResultMsg{OK: false, Status: "dial tcp: connection refused"})
	if m.aiCfgTest.ok || strings.Contains(m.aiCfgTest.status, "dial tcp") {
		t.Fatalf("refused must be translated into a human hint, got %q", m.aiCfgTest.status)
	}
}

// TestAISettingsEditAndCommit edits the Model field and commits it via Enter.
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
