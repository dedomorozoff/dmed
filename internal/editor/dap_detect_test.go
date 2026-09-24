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
	if dc.AdapterCmd != "php" || dc.AdapterMode != "dbgp" {
		t.Errorf("adapter = %q/%q, want php/dbgp", dc.AdapterCmd, dc.AdapterMode)
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
	if got := m.dapDebugCfg().AdapterMode; got != "dbgp" {
		t.Errorf("dapDebugCfg().AdapterMode = %q, want dbgp", got)
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
	dc := config.DebugConfig{AdapterCmd: "php", AdapterMode: "dbgp"}
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

func TestDapJsDebugScriptFound(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DMED_JS_DEBUG", filepath.Join(dir, "dap.js"))
	if err := os.WriteFile(filepath.Join(dir, "dap.js"), []byte("// adapter"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := dapJsDebugScript(); got == "" {
		t.Fatal("DMED_JS_DEBUG pointing at dap.js must be honored")
	}
	t.Setenv("DMED_JS_DEBUG", "") // empty falls through to the extension scan

	// A VS Code layout: ~/.vscode/extensions/ms-vscode.js-debug-1.99.0/src/dap.js
	home, _ := os.UserHomeDir()
	extBase := filepath.Join(home, ".vscode", "extensions")
	if err := os.MkdirAll(filepath.Join(extBase, "ms-vscode.js-debug-1.99.0", "src"), 0o755); err != nil {
		t.Skipf("cannot create fake extension dir: %v", err)
	}
	script := filepath.Join(extBase, "ms-vscode.js-debug-1.99.0", "src", "dap.js")
	if err := os.WriteFile(script, []byte("// adapter"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(filepath.Join(extBase, "ms-vscode.js-debug-1.99.0"))
	if got := dapJsDebugScript(); got != script {
		t.Errorf("dapJsDebugScript() = %q, want %q", got, script)
	}

	// Unknown extension publishers must not match.
	if err := os.MkdirAll(filepath.Join(extBase, "someoneelse.thing-1.0.0", "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(filepath.Join(extBase, "someoneelse.thing-1.0.0"))
	if got := dapJsDebugScript(); got != script {
		t.Errorf("dapJsDebugScript() = %q, want %q (only ms-vscode.js-debug* matches)", got, script)
	}
}

func TestDapLangPresetJs(t *testing.T) {
	// Pin the adapter script so the test does not depend on a locally
	// installed VS Code extension; the not-installed case is covered by
	// TestDapLangPresetJsMissingAdapter.
	script := filepath.Join(t.TempDir(), "dap.js")
	if err := os.WriteFile(script, []byte("// adapter"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DMED_JS_DEBUG", script)
	for _, name := range []string{"app.js", "app.mjs", "app.cjs", "app.jsx", "app.ts", "app.mts", "app.tsx"} {
		m := newEditorWithFile(t, name, "console.log(1)\n")
		dc, ok := m.dapLangPreset()
		if !ok {
			t.Fatalf("%s preset not detected", name)
		}
		if dc.AdapterCmd != "node" || dc.AdapterMode != "stdio" {
			t.Errorf("%s: adapter = %q/%q, want node/stdio", name, dc.AdapterCmd, dc.AdapterMode)
		}
		if dc.LaunchType != "node" {
			t.Errorf("%s: launch_type = %q, want node", name, dc.LaunchType)
		}
		if !strings.Contains(dc.LaunchJSON, `"node"`) {
			t.Errorf("%s: launch_json = %q, want a node launch body", name, dc.LaunchJSON)
		}
		// The located adapter script rides in adapter_args.
		if dc.AdapterArgs != script {
			t.Errorf("%s: adapter_args = %q, want %q", name, dc.AdapterArgs, script)
		}
		// The active file, not its directory, is what node runs.
		m.dapDeduced = &dc
		if got := m.dapProgram(); filepath.Ext(got) != filepath.Ext(name) || got == filepath.Dir(got) {
			t.Errorf("%s: dapProgram() = %q, want the active file", name, got)
		}
	}
}

func TestDapLangPresetJsMissingAdapter(t *testing.T) {
	// The preset must agree with the adapter lookup: adapter_cmd stays empty
	// exactly when the vscode-js-debug adapter is not installed anywhere we
	// know of, so the start fails with the actionable error instead of
	// spawning a bare node. On machines where the adapter IS installed this
	// degenerates to the node/dap.js shape.
	t.Setenv("DMED_JS_DEBUG", "")
	m := newEditorWithFile(t, "app.js", "console.log(1)\n")
	m.root = t.TempDir() // a project without .dmed.conf
	dc, ok := m.dapLangPreset()
	if !ok {
		t.Fatal("js preset expected regardless of adapter availability")
	}
	if script := dapJsDebugScript(); script == "" {
		if dc.AdapterCmd != "" {
			t.Errorf("adapter_cmd = %q, want empty (adapter missing)", dc.AdapterCmd)
		}
		if err := dapJsDebugError(); err == nil || !strings.Contains(err.Error(), "vscode-js-debug") {
			t.Errorf("dapJsDebugError() = %v, want an actionable message", err)
		}
	} else if dc.AdapterCmd != "node" || !strings.HasSuffix(filepath.ToSlash(dc.AdapterArgs), "dap.js") {
		t.Errorf("adapter = %q/%q with script %q, want node/<dap.js>", dc.AdapterCmd, dc.AdapterArgs, script)
	}
}
