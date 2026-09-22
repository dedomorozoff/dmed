package editor

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Layout-independent chords. On the Windows Console API with a non-US
// keyboard layout a Ctrl/Alt+letter chord arrives with the Cyrillic Code AND
// Text while holding the modifier. handleKey must still match "ctrl+x" et al.
// instead of falling through and typing the letter.
func TestRUChordsFire(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "a.txt", "line1\nline2\nline3\n")

	cases := []struct {
		name  string
		setup func(*testing.T) Model
		msg   tea.KeyPressMsg // RU-layout form: Cyrillic Code (+Text), no BaseCode
		check func(Model) bool
	}{
		{"ctrl+g via Cyrillic Code opens git panel",
			func(*testing.T) Model { return New(dir) },
			tea.KeyPressMsg{Code: 'п', Mod: tea.ModCtrl},
			func(m Model) bool { return m.gitOpen }},
		{"ctrl+g with Cyrillic Text bundled opens git panel",
			func(*testing.T) Model { return New(dir) },
			tea.KeyPressMsg{Code: 'п', Text: "п", Mod: tea.ModCtrl},
			func(m Model) bool { return m.gitOpen }},
		{"ctrl+g via raw BEL byte opens git panel",
			func(*testing.T) Model { return New(dir) },
			tea.KeyPressMsg{Text: "\x07"},
			func(m Model) bool { return m.gitOpen }},
		{"ctrl+b focuses the tree",
			func(*testing.T) Model { return New(dir) },
			tea.KeyPressMsg{Code: 'и', Mod: tea.ModCtrl},
			func(m Model) bool { return m.treeFocus }},
		{"ctrl+shift+p opens palette",
			func(*testing.T) Model { return New(dir) },
			tea.KeyPressMsg{Code: 'з', Mod: tea.ModCtrl | tea.ModShift},
			func(m Model) bool { return m.paletteOpen }},
		{"alt+a opens chat",
			func(*testing.T) Model { return New(dir) },
			tea.KeyPressMsg{Code: 'ф', Mod: tea.ModAlt},
			func(m Model) bool { return m.chatOpen }},
		{"ctrl+s saves a dirty buffer",
			func(*testing.T) Model {
				m := New(dir + "/a.txt")
				m.cur().buf.Insert('X')
				return m
			},
			tea.KeyPressMsg{Code: 'ы', Mod: tea.ModCtrl},
			func(Model) bool {
				got, err := os.ReadFile(dir + "/a.txt")
				return err == nil && strings.Contains(string(got), "X")
			}},
		{"ctrl+z undoes an insert",
			func(*testing.T) Model {
				m := New(dir)
				m.cur().buf.Insert('X')
				return m
			},
			tea.KeyPressMsg{Code: 'я', Mod: tea.ModCtrl},
			func(m Model) bool { return !strings.Contains(m.cur().buf.Text(), "X") }},
	}
	for _, c := range cases {
		m := c.setup(t)
		m.width, m.height = 80, 48
		m = press(m, c.msg)
		if !c.check(m) {
			t.Errorf("%s: chord did not fire", c.name)
		}
	}
}