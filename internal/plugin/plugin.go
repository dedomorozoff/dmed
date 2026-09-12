// Package plugin provides a Lua-based plugin system for the editor.
//
// A plugin is a .lua file that runs against an embedded gopher-lua VM (pure
// Go). It can register keybindings, command-palette commands and event
// handlers, and read/edit the active buffer through the `dmed` global.
//
// The editor model (which is a value in Bubble Tea) is passed to each
// invocation via the Host interface, so a plugin's buffer edits land directly
// in the model instance being updated.
package plugin

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// Host is the editor surface a plugin drives. It is implemented by the editor
// model and provided to every plugin invocation.
type Host interface {
	Text() string
	SetText(s string)
	LineCount() int
	Line(i int) string
	Cursor() (line, col int)
	SetCursor(line, col int)
	Insert(s string)
	Status(msg string)
	Save()
}

// Command is a plugin-registered command-palette entry.
type Command struct {
	ID    string
	Title string
	Desc  string
	ref   lua.LValue
	p     *Plugin
}

// Plugin is a single loaded .lua plugin.
type Plugin struct {
	path          string
	name          string
	L             *lua.LState
	mgr           *Manager
	keyHandlers   map[string][]lua.LValue
	commandRefs   map[string]*Command
	eventHandlers map[string][]lua.LValue
}

// Manager owns all loaded plugins.
type Manager struct {
	plugins  []*Plugin
	commands []*Command
}

// New returns an empty manager.
func New() *Manager {
	return &Manager{}
}

// Load scans dir for *.lua files (sorted) and runs each one to let it register
// keybindings, commands and event handlers. Plugins that fail to load are
// skipped and recorded in Errors.
func (m *Manager) Load(dir string) []error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var paths []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".lua") {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(paths)
	var errs []error
	for _, p := range paths {
		if err := m.loadFile(p); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func (m *Manager) loadFile(path string) error {
	p, err := m.build(path)
	if err != nil {
		return err
	}
	m.plugins = append(m.plugins, p)
	return nil
}

// build parses a .lua plugin file into a fresh Plugin without registering it.
func (m *Manager) build(path string) (*Plugin, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	p := &Plugin{
		path:          path,
		name:          filepath.Base(path),
		L:             lua.NewState(),
		mgr:           m,
		keyHandlers:   map[string][]lua.LValue{},
		commandRefs:   map[string]*Command{},
		eventHandlers: map[string][]lua.LValue{},
	}
	installRegAPI(p)
	if err := p.L.DoString(string(src)); err != nil {
		p.L.Close()
		return nil, err
	}
	return p, nil
}

// Names returns the sorted names of successfully loaded plugins.
func (m *Manager) Names() []string {
	var out []string
	for _, p := range m.plugins {
		out = append(out, p.name)
	}
	sort.Strings(out)
	return out
}

// HasBinding reports whether any plugin handles the given key.
func (m *Manager) HasBinding(key string) bool {
	for _, p := range m.plugins {
		if len(p.keyHandlers[key]) > 0 {
			return true
		}
	}
	return false
}

// RunBinding invokes every plugin handler registered for key with host as the
// editor context. It returns true if at least one handler ran.
func (m *Manager) RunBinding(host Host, key string) bool {
	ran := false
	for _, p := range m.plugins {
		for _, fn := range p.keyHandlers[key] {
			m.invoke(p, host, fn)
			ran = true
		}
	}
	return ran
}

// Commands returns the plugin-registered palette commands (in load order).
func (m *Manager) Commands() []*Command {
	return m.commands
}

func (m *Manager) addCommand(c *Command) {
	m.commands = append(m.commands, c)
}

// RunCommand invokes the plugin command with the given id, if registered.
func (m *Manager) RunCommand(host Host, id string) bool {
	for _, p := range m.plugins {
		if c, ok := p.commandRefs[id]; ok {
			m.invoke(p, host, c.ref)
			return true
		}
	}
	return false
}

// Emit dispatches an event to all handlers registered for it.
func (m *Manager) Emit(host Host, event string) {
	for _, p := range m.plugins {
		for _, fn := range p.eventHandlers[event] {
			m.invoke(p, host, fn)
		}
	}
}

// EmitTo dispatches an event only to handlers registered by the plugin loaded
// from path.
func (m *Manager) EmitTo(host Host, path, event string) {
	for _, p := range m.plugins {
		if p.path == path {
			for _, fn := range p.eventHandlers[event] {
				m.invoke(p, host, fn)
			}
			return
		}
	}
}

// Reload replaces the plugin loaded from path with a fresh instance, dropping
// its previously registered keybindings, commands and event handlers. It
// returns an error if the new source fails to parse.
func (m *Manager) Reload(path string) error {
	path = filepath.Clean(path)
	p, err := m.build(path)
	if err != nil {
		return err
	}
	for i, old := range m.plugins {
		if filepath.Clean(old.path) == path {
			m.removePlugin(i)
			break
		}
	}
	m.plugins = append(m.plugins, p)
	return nil
}

func (m *Manager) removePlugin(i int) {
	p := m.plugins[i]
	m.plugins = append(m.plugins[:i], m.plugins[i+1:]...)
	var kept []*Command
	for _, c := range m.commands {
		if c.p != p {
			kept = append(kept, c)
		}
	}
	m.commands = kept
	p.L.Close()
}

// Remove drops the plugin loaded from path (if any), including its commands.
func (m *Manager) Remove(path string) {
	path = filepath.Clean(path)
	for i, p := range m.plugins {
		if filepath.Clean(p.path) == path {
			m.removePlugin(i)
			return
		}
	}
}

// invoke runs a Lua function after binding the `dmed` API to the given host.
func (m *Manager) invoke(p *Plugin, host Host, fn lua.LValue) {
	installHostAPI(p, host)
	_ = p.L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true})
}

// installRegAPI registers the host-independent plugin API: on_key, command, on.
func installRegAPI(p *Plugin) {
	dmed := p.L.NewTable()
	p.L.SetGlobal("dmed", dmed)

	p.L.SetField(dmed, "on_key", p.L.NewFunction(func(L *lua.LState) int {
		key := L.CheckString(1)
		fn := L.CheckAny(2)
		p.keyHandlers[key] = append(p.keyHandlers[key], fn)
		return 0
	}))

	p.L.SetField(dmed, "command", p.L.NewFunction(func(L *lua.LState) int {
		id := L.CheckString(1)
		title := L.CheckString(2)
		desc := L.CheckString(3)
		fn := L.CheckAny(4)
		c := &Command{ID: id, Title: title, Desc: desc, ref: fn, p: p}
		p.commandRefs[id] = c
		p.mgr.addCommand(c)
		return 0
	}))

	p.L.SetField(dmed, "on", p.L.NewFunction(func(L *lua.LState) int {
		event := L.CheckString(1)
		fn := L.CheckAny(2)
		p.eventHandlers[event] = append(p.eventHandlers[event], fn)
		return 0
	}))
}

// installHostAPI binds the buffer/edit functions of `dmed` to host for this
// invocation. Registration functions are left as-is.
func installHostAPI(p *Plugin, host Host) {
	dmed := p.L.GetGlobal("dmed").(*lua.LTable)
	str := func(s string) lua.LValue { return lua.LString(s) }

	p.L.SetField(dmed, "text", p.L.NewFunction(func(L *lua.LState) int {
		L.Push(str(host.Text()))
		return 1
	}))
	p.L.SetField(dmed, "set_text", p.L.NewFunction(func(L *lua.LState) int {
		host.SetText(L.CheckString(1))
		return 0
	}))
	p.L.SetField(dmed, "line_count", p.L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LNumber(host.LineCount()))
		return 1
	}))
	p.L.SetField(dmed, "line", p.L.NewFunction(func(L *lua.LState) int {
		L.Push(str(host.Line(int(L.CheckNumber(1)))))
		return 1
	}))
	p.L.SetField(dmed, "cursor", p.L.NewFunction(func(L *lua.LState) int {
		l, c := host.Cursor()
		t := L.NewTable()
		t.RawSetString("line", lua.LNumber(l))
		t.RawSetString("col", lua.LNumber(c))
		L.Push(t)
		return 1
	}))
	p.L.SetField(dmed, "set_cursor", p.L.NewFunction(func(L *lua.LState) int {
		host.SetCursor(int(L.CheckNumber(1)), int(L.CheckNumber(2)))
		return 0
	}))
	p.L.SetField(dmed, "insert", p.L.NewFunction(func(L *lua.LState) int {
		host.Insert(L.CheckString(1))
		return 0
	}))
	p.L.SetField(dmed, "status", p.L.NewFunction(func(L *lua.LState) int {
		host.Status(L.CheckString(1))
		return 0
	}))
	p.L.SetField(dmed, "save", p.L.NewFunction(func(L *lua.LState) int {
		host.Save()
		return 0
	}))
}
