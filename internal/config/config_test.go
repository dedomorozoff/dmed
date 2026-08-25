package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()
	if cfg.Editor.TabWidth != 4 {
		t.Errorf("tab_width = %d, want 4", cfg.Editor.TabWidth)
	}
	if cfg.Editor.SyntaxTheme != "monokai" {
		t.Errorf("syntax_theme = %q, want monokai", cfg.Editor.SyntaxTheme)
	}
	if !cfg.Editor.LineNumbers {
		t.Error("line_numbers should default to true")
	}
	if cfg.AI.OllamaURL != "http://localhost:11434" {
		t.Errorf("ollama_url = %q", cfg.AI.OllamaURL)
	}
	if cfg.UI.TreeWidth != 25 {
		t.Errorf("tree_width = %d, want 25", cfg.UI.TreeWidth)
	}
}

func TestParseINI(t *testing.T) {
	input := `[editor]
tab_width = 2
syntax_theme = dracula
line_numbers = false
skipped_dirs = .git,node_modules,vendor

[ai]
model = qwen2.5-coder:7b
ollama_url = http://localhost:11434

[ui]
tree_width = 30
`
	sections := parseINI(strings.NewReader(input))

	if sections["editor"]["tab_width"] != "2" {
		t.Errorf("editor.tab_width = %q, want 2", sections["editor"]["tab_width"])
	}
	if sections["editor"]["syntax_theme"] != "dracula" {
		t.Errorf("editor.syntax_theme = %q, want dracula", sections["editor"]["syntax_theme"])
	}
	if sections["ai"]["model"] != "qwen2.5-coder:7b" {
		t.Errorf("ai.model = %q", sections["ai"]["model"])
	}
	if sections["ui"]["tree_width"] != "30" {
		t.Errorf("ui.tree_width = %q", sections["ui"]["tree_width"])
	}
}

func TestParseINIComments(t *testing.T) {
	input := `# this is a comment
; so is this
[editor]
tab_width = 8
`
	sections := parseINI(strings.NewReader(input))
	if sections["editor"]["tab_width"] != "8" {
		t.Errorf("tab_width = %q, want 8", sections["editor"]["tab_width"])
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	content := `[editor]
tab_width = 2
syntax_theme = dracula

[ai]
model = llama3
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Defaults()
	loadFile(path, &cfg)

	if cfg.Editor.TabWidth != 2 {
		t.Errorf("tab_width = %d, want 2", cfg.Editor.TabWidth)
	}
	if cfg.Editor.SyntaxTheme != "dracula" {
		t.Errorf("syntax_theme = %q, want dracula", cfg.Editor.SyntaxTheme)
	}
	if cfg.AI.Model != "llama3" {
		t.Errorf("model = %q, want llama3", cfg.AI.Model)
	}
	// Defaults should be preserved for unset values
	if cfg.UI.TreeWidth != 25 {
		t.Errorf("tree_width = %d, want 25 (default)", cfg.UI.TreeWidth)
	}
}

func TestLoadProjectOverridesGlobal(t *testing.T) {
	globalDir := t.TempDir()
	globalPath := filepath.Join(globalDir, ".dmed.conf")
	os.WriteFile(globalPath, []byte("[editor]\ntab_width = 2\n"), 0o644)

	projectDir := t.TempDir()
	projectPath := filepath.Join(projectDir, ".dmed.conf")
	os.WriteFile(projectPath, []byte("[editor]\ntab_width = 8\n"), 0o644)

	cfg := Defaults()
	loadFile(globalPath, &cfg)
	loadFile(projectPath, &cfg)

	if cfg.Editor.TabWidth != 8 {
		t.Errorf("tab_width = %d, want 8 (project overrides global)", cfg.Editor.TabWidth)
	}
}
