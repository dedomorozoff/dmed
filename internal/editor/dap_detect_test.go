package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dmed/internal/config"
)

func newEditorWithFile(t *testing.T, name, content string) Model {
	t.Helper()
	dir := t.TempDir()
	chdir(t, dir)
	path := writeTemp(t, dir, name, content)
	m := New(path)
	m.width, m.height = 80, 24
	return m
}

func TestStatusBarShowsLanguage(t *testing.T) {
	m := newEditorWithFile(t, "main.go", "package main\n")
	if got := stripANSI(m.statusBar()); !strings.Contains(got, "go ") {
		t.Fatalf("global status bar %q must carry the language tag", got)
	}

	m2 := newEditorWithFile(t, "index.php", "<?php\n")
	if got := stripANSI(m2.statusBar()); !strings.Contains(got, "php ") {
		t.Fatalf("pane status bar %q must carry the language tag", got)
	}

	m3 := newEditorWithFile(t, "README", "plain\n")
	if got := stripANSI(m3.statusBar()); strings.Contains(got, "lang") {
		t.Fatalf("status bar %q must not invent a language for an unknown file", got)
	}
}

func TestDapLangPresetPHP(t *testing.T) {
	m := newEditorWithFile(t, "index.php", "<?php\n")
	dc, ok := m.dapLangPreset()
	if !ok {
		t.Fatal("php preset not detected")
	}
	if dc.AdapterCmd != "xdebug" || dc.AdapterMode != "connect" {
		t.Errorf("adapter = %q/%q, want xdebug/connect", dc.AdapterCmd, dc.AdapterMode)
	}
	if dc.AdapterArgs != "127.0.0.1:9003" {
		t.Errorf("adapter_args = %q, want 127.0.0.1:9003", dc.AdapterArgs)
	}
	if dc.LaunchType != "php" {
		t.Errorf("launch_type = %q, want php", dc.LaunchType)
	}
	if !strings.Contains(dc.LaunchJSON, `"php"`) {
		t.Errorf("launch_json = %q, want a php launch body", dc.LaunchJSON)
	}

	m.dapDeduced = &dc // startDebugging stores the merge before launching
	if got := m.dapDebugCfg().AdapterMode; got != "connect" {
		t.Errorf("dapDebugCfg().AdapterMode = %q, want connect", got)
	}
}

func TestDapLangPresetGo(t *testing.T) {
	m := newEditorWithFile(t, "main.go", "package main\n")
	dc, ok := m.dapLangPreset()
	if !ok {
		t.Fatal("go preset not detected")
	}
	if dc.AdapterCmd != "dlv" || dc.AdapterMode != "reverse" {
		t.Errorf("adapter = %q/%q, want dlv/reverse", dc.AdapterCmd, dc.AdapterMode)
	}
	if dc.LaunchType != "go" {
		t.Errorf("launch_type = %q, want go", dc.LaunchType)
	}
}

func TestDapLangPresetUnknownAndDisabled(t *testing.T) {
	m := newEditorWithFile(t, "notes.txt", "plain\n")
	if _, ok := m.dapLangPreset(); ok {
		t.Fatal("plain text must not be auto-detected")
	}

	m2 := newEditorWithFile(t, "index.php", "<?php\n")
	m2.cfg.Debug.AutoDetect = false
	if _, ok := m2.dapLangPreset(); ok {
		t.Fatal("auto_detect=false must disable the preset")
	}
}

func TestDapLangPresetRespectsExplicitConfig(t *testing.T) {
	m := newEditorWithFile(t, "index.php", "<?php\n")
	m.cfg = config.Load(m.root)
	m.cfg.Debug.AdapterCmd = "debugpy-adapter" // a pinned manual adapter
	dc, ok := m.dapLangPreset()
	if !ok {
		t.Fatal("php preset expected even with a pinned adapter")
	}
	if dc.AdapterCmd != "debugpy-adapter" {
		t.Errorf("adapter_cmd = %q, want the user's debugpy-adapter", dc.AdapterCmd)
	}
	m.dapDeduced = &dc
	if got := m.dapDebugCfg().AdapterCmd; got != "debugpy-adapter" {
		t.Errorf("dapDebugCfg().AdapterCmd = %q", got)
	}
	if got := m.dapDebugCfg().LaunchType; got != "php" {
		t.Errorf("dapDebugCfg().LaunchType = %q, want php (preset still fills unset fields)", got)
	}
}

func TestDapDeducedClearedOnRelease(t *testing.T) {
	m := newEditorWithFile(t, "index.php", "<?php\n")
	dc := config.DebugConfig{AdapterCmd: "xdebug"}
	m.dapDeduced = &dc
	m.dapRelease()
	if m.dapDeduced != nil {
		t.Fatal("dapRelease must clear the deduced config")
	}
}

func TestDapAutoDetectConfigKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	if err := os.WriteFile(path, []byte("[debug]\nauto_detect = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Load(dir)
	if cfg.Debug.AutoDetect {
		t.Fatal("auto_detect = false must parse")
	}
	cfg2 := config.Defaults()
	if !cfg2.Debug.AutoDetect {
		t.Fatal("auto_detect must default to on")
	}
}
