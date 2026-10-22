package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/i18n"
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

// TestSearchEverywhereFindsFilesAndSettings: the JetBrains-style double
// Shift searches commands, project files and settings in one list.
func TestSearchEverywhereFindsFilesAndSettings(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "alpha.txt", "hi\n")
	writeTemp(t, dir, "beta.go", "package main\n")

	m := New(f)
	m.width, m.height = 80, 24
	m.root = dir

	// A single Shift tap must not open it; the second one must.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyLeftShift})
	if m.paletteOpen {
		t.Fatal("single shift must not open the palette")
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyRightShift})
	if !m.paletteOpen {
		t.Fatal("double shift must open the palette")
	}
	if !m.paletteAll {
		t.Fatal("double shift must open the search-everywhere palette")
	}

	// Files are in the list.
	m = typeStr(m, "beta.go")
	hits := m.filterPalette()
	if len(hits) == 0 || hits[0].kind != paletteKindFile {
		t.Fatalf("expected the file as top match, got: %+v", hits)
	}
	if hits[0].title != "beta.go" {
		t.Fatalf("file title = %q", hits[0].title)
	}

	// Enter opens the file in a tab.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.paletteOpen {
		t.Fatal("the palette must close after picking a file")
	}
	found := false
	for _, tab := range m.tabs {
		if strings.HasSuffix(tab.path, "beta.go") {
			found = true
		}
	}
	if !found {
		t.Fatal("the picked file must be open in a tab")
	}

	// Settings are in the list too, with their current value.
	m.startSearchEverywhere()
	m = typeStr(m, "tab_width")
	hits = m.filterPalette()
	if len(hits) == 0 || hits[0].kind != paletteKindSetting {
		t.Fatalf("expected the setting as top match, got: %+v", hits)
	}
	if hits[0].title != "editor.tab_width" {
		t.Fatalf("setting title = %q", hits[0].title)
	}
}

// TestSearchEverywhereRendersKinds: the results say what they are — a file
// and a setting look different from a command.
func TestSearchEverywhereRendersKinds(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "alpha.txt", "hi\n")

	m := New(f)
	m.width, m.height = 80, 24
	m.root = dir

	m.startSearchEverywhere()
	m = typeStr(m, "alpha.txt")
	v := m.View()
	if !strings.Contains(v.Content, "[file]") {
		t.Fatalf("the file result must be marked:\n%s", v.Content)
	}
	if !strings.Contains(v.Content, "search everywhere") {
		t.Fatalf("the prompt must say that everything is searched:\n%s", v.Content)
	}

	m.paletteQ = nil
	m = typeStr(m, "tab_width")
	v = m.View()
	if !strings.Contains(v.Content, "[setting]") {
		t.Fatalf("the setting result must be marked:\n%s", v.Content)
	}
}

// TestSearchEverywhereBindings: most terminals never report a bare modifier
// press, so the double Shift cannot be the only way in. Alt+P survives even
// legacy input (ESC+p), Ctrl+Shift+P needs a terminal that tells Shift apart.
func TestSearchEverywhereBindings(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "alpha.txt", "hi\n")

	for _, tc := range []struct {
		name string
		msg  tea.KeyPressMsg
	}{
		{"alt+p", tea.KeyPressMsg{Code: 'p', Mod: tea.ModAlt}},
		{"ctrl+shift+p", tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl | tea.ModShift}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(f)
			m.width, m.height = 80, 24
			m.root = dir

			m = press(m, tc.msg)
			if !m.paletteOpen {
				t.Fatalf("%s must open the palette", tc.name)
			}
			if !m.paletteAll {
				t.Fatalf("%s must open the search-everywhere palette", tc.name)
			}
			m = typeStr(m, "alpha.txt")
			hits := m.filterPalette()
			if len(hits) == 0 || hits[0].kind != paletteKindFile {
				t.Fatalf("expected the file as top match, got: %+v", hits)
			}
		})
	}
}

// TestCommandPaletteStaysCommandsOnly: Ctrl+P keeps searching commands only,
// the way JetBrains keeps "find action" apart from "search everywhere".
func TestCommandPaletteStaysCommandsOnly(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "alpha.txt", "hi\n")

	m := New(f)
	m.width, m.height = 80, 24
	m.root = dir

	m = press(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if !m.paletteOpen || m.paletteAll {
		t.Fatal("ctrl+p must open the command palette")
	}
	m = typeStr(m, "alpha.txt")
	if hits := m.filterPalette(); len(hits) != 0 {
		t.Fatalf("the command palette must not offer files, got: %+v", hits)
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

func TestPaletteNewFolderCommand(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "existing.txt", "hello\n")

	m := New(f)
	m.width, m.height = 80, 24

	m = press(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if !m.paletteOpen {
		t.Fatal("palette must be open after Ctrl+P")
	}

	m = typeStr(m, "new folder")
	hits := m.filterPalette()
	if len(hits) == 0 || hits[0].id != "new_folder" {
		t.Fatalf("expected 'new_folder' command as top match, got: %+v", hits)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.paletteOpen {
		t.Fatal("palette must close after executing new folder command")
	}
	if !m.promptOpen || !m.promptNewFolder {
		t.Fatalf("new folder command must open the prompt in new-folder mode")
	}

	folder := filepath.Join(dir, "brand_new_dir")
	m = typeStr(m, folder)
	v := m.View()
	if !strings.Contains(v.Content, "new folder:") {
		t.Fatalf("view must render new-folder prompt label:\n%s", v.Content)
	}

	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.promptOpen {
		t.Fatal("prompt must close after entering path")
	}
	fi, err := os.Stat(folder)
	if err != nil || !fi.Mode().IsDir() {
		t.Fatalf("new folder must create directory %s, err=%v", folder, err)
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

func TestPaletteSwitchLanguage(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "lang.txt", "x\n")
	m := New(f)
	m.width, m.height = 80, 24
	m.root = dir

	// Filter for the Russian language command and run it.
	m.startPalette()
	// Palette input normalizes Cyrillic to QWERTY, so filter by the Latin
	// description word.
	m = typeStr(m, "language")
	hits := m.filterPalette()
	if len(hits) == 0 || hits[0].id != "lang_select" {
		t.Fatalf("expected 'lang_select' hit, got: %+v", hits)
	}
	m.paletteSel = 0
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // opens the language chooser
	if !m.langChooserOpen {
		t.Fatal("language chooser must open")
	}
	// The chooser must actually be visible with both languages.
	content := m.View().Content
	if !strings.Contains(content, "Русский") || !strings.Contains(content, "English") {
		t.Fatalf("chooser panel not rendering languages:\n%s", content)
	}
	// Navigate down to Русский and select it.
	m = press(m, tea.KeyPressMsg{Text: "j"})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if got := m.tr.Lang(); got != i18n.Ru {
		t.Fatalf("translator lang=%q, want ru", got)
	}
	// The choice must persist to the project config.
	data, err := os.ReadFile(filepath.Join(dir, ".dmed.conf"))
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if !strings.Contains(string(data), "lang = ru") {
		t.Fatalf("lang not persisted:\n%s", data)
	}
}
