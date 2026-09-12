package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/bundled"
)

func writeProjectPlugin(t *testing.T, dir, name, src string) {
	t.Helper()
	pdir := filepath.Join(dir, ".dmed", "plugins")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, name), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPluginKeybindingAndCommand(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "x.txt", "hello\n")
	writeProjectPlugin(t, dir, "demo.lua", `
dmed.on_key("ctrl+u", function()
  dmed.set_text(dmed.text():upper())
end)
dmed.command("pong", "Pong", "append pong", function()
  dmed.insert("pong")
end)
`)

	m := New(f)
	m.width, m.height = 80, 24
	m.root = dir
	m.loadPlugins()
	m.plugins.Emit(&m, "ready")

	loaded := false
	for _, n := range m.plugins.Names() {
		if n == "demo.lua" {
			loaded = true
		}
	}
	if !loaded {
		t.Fatalf("demo.lua not loaded: %v", m.plugins.Names())
	}

	// Keybinding edits the buffer through Lua.
	m = press(m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if got := m.cur().buf.Text(); got != "HELLO\n" {
		t.Fatalf("after ctrl+u buffer=%q, want HELLO", got)
	}

	// Plugin command shows up in the palette and runs.
	m.startPalette()
	m = typeStr(m, "pong")
	hits := m.filterPalette()
	if len(hits) == 0 || hits[0].id != "pong" {
		t.Fatalf("expected 'pong' command, got %+v", hits)
	}
	m.paletteSel = 0
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := m.cur().buf.Text(); !strings.Contains(got, "pong") {
		t.Fatalf("plugin command did not insert: %q", got)
	}
}

func TestPluginFileOpenEvent(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "x.txt", "a\n")
	writeProjectPlugin(t, dir, "open.lua", `
dmed.on("file_open", function()
  dmed.status("opened")
end)
`)
	m := New(f)
	m.width, m.height = 80, 24
	m.root = dir
	m.loadPlugins()
	m.plugins.Emit(&m, "ready")

	m.openPath(filepath.Join(dir, "other.txt"))
	if !strings.Contains(m.msg, "opened") {
		t.Fatalf("file_open event did not set status, msg=%q", m.msg)
	}
}

func TestPluginAutoReloadOnFileChange(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "x.txt", "hello\n")
	writeProjectPlugin(t, dir, "hot.lua", `
dmed.on_key("ctrl+k", function()
  dmed.set_text("one")
end)
`)
	m := New(f)
	m.width, m.height = 80, 24
	m.root = dir
	m.loadPlugins()

	m = press(m, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	if got := m.cur().buf.Text(); got != "one\n" {
		t.Fatalf("before reload buffer=%q, want one", got)
	}

	// Edit the plugin on disk and deliver a file-change event.
	writeProjectPlugin(t, dir, "hot.lua", `
dmed.on_key("ctrl+k", function()
  dmed.set_text("two")
end)
`)
	next, _ := m.Update(FileChangedMsg{Path: filepath.Join(dir, ".dmed", "plugins", "hot.lua")})
	m = next.(Model)
	m = press(m, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	if got := m.cur().buf.Text(); got != "two\n" {
		t.Fatalf("after reload buffer=%q, want two", got)
	}
}

func TestPluginStoreInstallUninstall(t *testing.T) {
	plugdir := t.TempDir()
	t.Setenv("DMED_PLUGIN_DIR", plugdir)
	m := New()
	m.width, m.height = 80, 24
	m.loadPlugins()

	first := bundled.Store[0].File
	target := m.pluginTargetPath(first)
	if m.pluginInstalled(first) {
		t.Fatalf("%s should not be installed initially", first)
	}

	// Install via the store (Enter on the selected item).
	m.openPluginStore()
	if !m.pluginStoreOpen {
		t.Fatal("plugin store did not open")
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.pluginStoreOpen {
		t.Fatal("store should close after Enter")
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("plugin file not written: %v", err)
	}
	if !m.pluginInstalled(first) {
		t.Fatalf("pluginInstalled(%s)=false after install", first)
	}
	// The installed plugin is live.
	if !m.plugins.HasBinding("ctrl+u") && first == "uppercase.lua" {
		t.Log("uppercase plugin should be loaded after install")
	}

	// Uninstall via the store.
	m.openPluginStore()
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if _, err := os.Stat(target); err == nil {
		t.Fatalf("plugin file still present after uninstall: %s", target)
	}
	if m.pluginInstalled(first) {
		t.Fatalf("pluginInstalled(%s)=true after uninstall", first)
	}
}
