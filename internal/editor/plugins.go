package editor

import (
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/buffer"
	"dmed/internal/bundled"
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
// global ~/.dmed/plugins first (or $DMED_PLUGIN_DIR for tests), then the
// project .dmed/plugins.
func (m Model) pluginDirs() []string {
	var dirs []string
	if pd := os.Getenv("DMED_PLUGIN_DIR"); pd != "" {
		dirs = append(dirs, pd)
	} else if home, err := os.UserHomeDir(); err == nil {
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
		if m.watcher != nil {
			_ = m.watcher.Watch(d)
		}
		for _, err := range m.plugins.Load(d) {
			m.msg = "plugin error: " + err.Error()
		}
	}
}

// isPluginPath reports whether path is a .lua file inside a plugin directory.
func (m Model) isPluginPath(path string) bool {
	if !strings.HasSuffix(path, ".lua") {
		return false
	}
	for _, d := range m.pluginDirs() {
		absD, _ := filepath.Abs(d)
		absP, _ := filepath.Abs(path)
		if absD != "" && strings.HasPrefix(absP, absD+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// reloadPlugin hot-reloads a single changed .lua plugin and re-emits "ready".
func (m *Model) reloadPlugin(path string) {
	if err := m.plugins.Reload(path); err != nil {
		m.msg = "plugin error: " + err.Error()
		return
	}
	m.plugins.EmitTo(m, path, "ready")
	m.msg = m.t("msg.plugin_reloaded", filepath.Base(path))
}

// openPluginStore opens the built-in plugin store panel and kicks off a
// background fetch of the remote plugin listing.
func (m *Model) openPluginStore() tea.Cmd {
	m.pluginStoreOpen = true
	m.pluginStoreSel = 0
	m.paletteOpen = false
	m.storeItems = storeEmbeddedItems()
	m.storeLoading = true
	m.storeErr = ""
	repo, dir, branch := m.pluginRepoConfig()
	return m.fetchStoreListCmd(repo, dir, branch)
}

// pluginTargetPath is where a bundled plugin file installs (global dir).
func (m Model) pluginTargetPath(file string) string {
	dirs := m.pluginDirs()
	if len(dirs) == 0 {
		return file
	}
	return filepath.Join(dirs[0], file)
}

// pluginInstalled reports whether a bundled plugin file is already present.
func (m Model) pluginInstalled(file string) bool {
	_, err := os.Stat(m.pluginTargetPath(file))
	return err == nil
}

// handlePluginStore handles keys while the plugin store is open.
func (m *Model) handlePluginStore(msg tea.KeyPressMsg) tea.Cmd {
	items := m.storeItems
	switch msg.String() {
	case "esc":
		m.pluginStoreOpen = false
	case "enter":
		if m.pluginStoreSel >= 0 && m.pluginStoreSel < len(items) {
			it := items[m.pluginStoreSel]
			m.pluginStoreOpen = false
			return m.activateStoreItem(it)
		}
		m.pluginStoreOpen = false
	case "up", "k":
		if len(items) > 0 {
			m.pluginStoreSel = (m.pluginStoreSel - 1 + len(items)) % len(items)
		}
	case "down", "j":
		if len(items) > 0 {
			m.pluginStoreSel = (m.pluginStoreSel + 1) % len(items)
		}
	}
	return nil
}

// activateStoreItem installs, uninstalls, or downloads a store item. Shared
// by the keyboard Enter handler and mouse clicks.
func (m *Model) activateStoreItem(it storeItem) tea.Cmd {
	if m.pluginInstalled(it.File) {
		m.uninstallStoreItem(it.File)
		return nil
	}
	if it.Remote {
		m.pendingStoreInstall = it.File
		m.msg = m.t("plugin.downloading", it.File)
		repo, dir, branch := m.pluginRepoConfig()
		return m.fetchStoreSourceCmd(repo, dir, branch, it.File)
	}
	m.installFromSource(it.File, bundled.Source(it.File))
	return nil
}

// installFromSource writes a plugin file to the global plugin dir and loads it.
func (m *Model) installFromSource(file, src string) {
	target := m.pluginTargetPath(file)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		m.msg = "plugin install error: " + err.Error()
		return
	}
	if err := os.WriteFile(target, []byte(src), 0o644); err != nil {
		m.msg = "plugin install error: " + err.Error()
		return
	}
	if m.watcher != nil {
		_ = m.watcher.Watch(filepath.Dir(target))
	}
	if m.plugins != nil {
		_ = m.plugins.Reload(target)
	}
	m.msg = m.t("msg.plugin_installed", file)
}

// uninstallStoreItem removes a plugin file and drops it from memory. The
// pendingPluginRemovals guard stops the watcher from reporting a reload error
// for the file we just deleted ourselves.
func (m *Model) uninstallStoreItem(file string) {
	target := m.pluginTargetPath(file)
	m.pendingPluginRemovals[target] = true
	_ = os.Remove(target)
	if m.plugins != nil {
		m.plugins.Remove(target)
	}
	m.msg = m.t("msg.plugin_uninstalled", file)
}
