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

// TestPaletteScrollKeepsSelectionVisible verifies the viewport follows paletteSel
// beyond the 8 visible rows.
func TestPaletteScrollKeepsSelectionVisible(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "palette.txt", "line1\nline2\n")

	m := New(f)
	m.width, m.height = 80, 24

	m = press(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if !m.paletteOpen {
		t.Fatal("palette must be open after Ctrl+P")
	}

	total := len(m.filterPalette())
	if total < 9 {
		t.Skipf("need >8 palette commands, got %d", total)
	}

	// Scroll all the way down; offset must clamp so the last item is visible.
	for i := 0; i < total; i++ {
		m.handlePalette(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if m.paletteSel != 0 {
		t.Fatalf("selection should wrap to 0 after %d downs, got %d", total, m.paletteSel)
	}
	if m.paletteOffset != 0 {
		t.Fatalf("offset should reset to 0 on wrap, got %d", m.paletteOffset)
	}

	// Now take total-1 downs: selection = last item, offset = total-8.
	for i := 0; i < total-1; i++ {
		m.handlePalette(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	want := total - 1
	if m.paletteSel != want {
		t.Fatalf("selection = %d, want %d", m.paletteSel, want)
	}
	if wantOffset := total - 8; m.paletteOffset != wantOffset {
		t.Fatalf("offset = %d, want %d", m.paletteOffset, wantOffset)
	}

	// The last command must be the one selected in the viewport window.
	display := m.filterPalette()
	if m.paletteOffset >= len(display) || display[m.paletteSel].id != display[len(display)-1].id {
		t.Fatalf("selected command should be the last one, sel=%d offset=%d len=%d",
			m.paletteSel, m.paletteOffset, len(display))
	}
}

// TestPaletteFilterBringsAISettings verifies the AI settings command surfaces
// through the query filter even though it sits beyond the visible window.
func TestPaletteFilterBringsAISettings(t *testing.T) {
	m := New()
	m.startPalette()

	m = typeStr(m, "preferences")
	hits := m.filterPalette()
	if len(hits) != 1 || hits[0].id != "ai_settings" {
		t.Fatalf("expected single 'ai_settings' hit, got: %+v", hits)
	}
}
