package editor

import (
	"os"
	"path/filepath"

	"dmed/internal/buffer"
	"dmed/internal/plugin"
)

// Model implements plugin.Host so Lua plugins can read/edit the active buffer.
var _ plugin.Host = (*Model)(nil)

func (m *Model) Text() string       { return m.cur().buf.Text() }
func (m *Model) SetText(s string)   { m.cur().buf = buffer.Load(s) }
func (m *Model) LineCount() int     { return m.cur().buf.LineCount() }
func (m *Model) Line(i int) string  { return string(m.cur().buf.LineAt(i)) }
func (m *Model) Cursor() (int, int) { return m.cur().buf.CurLine(), m.cur().buf.Col() }
func (m *Model) SetCursor(l, c int) { m.cur().buf.SetCursor(l, c) }
func (m *Model) Insert(s string)    { m.cur().buf.InsertText(s) }
func (m *Model) Status(msg string)  { m.msg = msg }
func (m *Model) Save()              { m.saveActive() }

// pluginDirs returns the plugin directories to search, in load order:
// global ~/.dmed/plugins first, then the project .dmed/plugins.
func (m Model) pluginDirs() []string {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".dmed", "plugins"))
	}
	if m.root != "" {
		dirs = append(dirs, filepath.Join(m.root, ".dmed", "plugins"))
	}
	return dirs
}

// loadPlugins initializes the plugin manager and loads every .lua plugin.
func (m *Model) loadPlugins() {
	m.plugins = plugin.New()
	for _, d := range m.pluginDirs() {
		for _, err := range m.plugins.Load(d) {
			m.msg = "plugin error: " + err.Error()
		}
	}
}
