package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestAISettingsProviderCycle verifies ←/→ flips the provider on the choice row.
func TestAISettingsProviderCycle(t *testing.T) {
	m := New()
	m.startAISettings()
	if !m.aiCfgOpen {
		t.Fatal("wizard should open")
	}
	if m.cfg.AI.Provider != "ollama" {
		t.Fatalf("prov in default = %q", m.cfg.AI.Provider)
	}
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.cfg.AI.Provider != "openai" {
		t.Fatalf("after right = %q, want openai", m.cfg.AI.Provider)
	}
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.cfg.AI.Provider != "ollama" {
		t.Fatalf("after left = %q, want ollama", m.cfg.AI.Provider)
	}
}

// TestAISettingsEditAndCommit edits the Model field and commits it via Enter.
func TestAISettingsEditAndCommit(t *testing.T) {
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

// TestAISettingsEscCloses verifies Esc exits, and Esc inside edit reverts.
func TestAISettingsEscCloses(t *testing.T) {
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
