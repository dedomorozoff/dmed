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

func TestPaletteNewFileCommand(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "existing.txt", "hello\n")

	m := New(f)
	m.width, m.height = 80, 24

	m = press(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if !m.paletteOpen {
		t.Fatal("palette must be open after Ctrl+P")
	}

	// Filter for "new file" and execute
	m = typeStr(m, "new file")
	hits := m.filterPalette()
	if len(hits) == 0 || hits[0].id != "new_file" {
		t.Fatalf("expected 'new_file' command as top match, got: %+v", hits)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.paletteOpen {
		t.Fatal("palette must close after executing new file command")
	}
	if !m.promptOpen || !m.promptNewFile {
		t.Fatalf("new file command must open the prompt in new-file mode")
	}

	m = typeStr(m, "brand_new.txt")
	v := m.View()
	if !strings.Contains(v.Content, "new file:") {
		t.Fatalf("view must render new-file prompt label:\n%s", v.Content)
	}

	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.promptOpen {
		t.Fatal("prompt must close after entering path")
	}
	found := false
	for _, t := range m.tabs {
		if strings.HasSuffix(t.path, "brand_new.txt") {
			found = true
		}
	}
	if !found {
		t.Fatal("new file must open a tab for brand_new.txt")
	}
}
