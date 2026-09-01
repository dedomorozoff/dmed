package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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
