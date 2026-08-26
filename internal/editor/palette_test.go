package editor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestCommandPaletteOpenFilterExecute(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "palette.txt", "line1\nline2\n")

	m := New(f)
	m.width, m.height = 80, 24

	// Open palette (Ctrl+P / F2)
	m = press(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if !m.paletteOpen {
		t.Fatal("palette must be open after Ctrl+P")
	}

	// Filter for "save"
	m = typeStr(m, "save")
	hits := m.filterPalette()
	if len(hits) == 0 || hits[0].id != "save" {
		t.Fatalf("expected 'save' command as top match, got: %+v", hits)
	}

	// View rendering
	v := m.View()
	if !strings.Contains(v.Content, "File: Save") {
		t.Fatalf("view must render palette panel with File: Save:\n%s", v.Content)
	}

	// Execute command (Enter)
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.paletteOpen {
		t.Fatal("palette must close after executing command")
	}
}
