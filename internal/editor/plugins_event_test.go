package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/buffer"
	"dmed/internal/events"
)

const watchLuaSrc = `
dmed.seen = {}
local function note(ev, arg)
  table.insert(dmed.seen, ev .. "|" .. (arg or ""))
  dmed.status(table.concat(dmed.seen, ";;"))
end
for _, ev in ipairs({"buffer:modified", "file:changed", "agent:updated"}) do
  dmed.on(ev, function(arg) note(ev, arg) end)
end
`

// pluginEventFixture builds a model with one plugin listening on the three
// bus events, wired the way New() does, and returns it with the temp plugin
// directory.
func pluginEventFixture(t *testing.T) (m Model) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "watch.lua"), []byte(watchLuaSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DMED_PLUGIN_DIR", dir)
	m = Model{
		bus:      events.New(),
		pluginCh: make(chan tea.Msg, 8),
		tabs:     []tab{{buf: buffer.New()}},
		panes:    []pane{{tabIdx: 0}},
	}
	m.loadPlugins()
	if errs := m.plugins.Names(); len(errs) != 1 {
		t.Fatalf("plugin not loaded: %v", errs)
	}
	m.plugins.SetWake(func() {
		select {
		case m.pluginCh <- pluginPumpMsg{}:
		default:
		}
	})
	m.wirePluginEvents()
	return m
}

// seenEvents parses the report string the plugin status keeps: one event
// record per ";;" separator, cumulative because the fixture is stateful.
func seenEvents(t *testing.T, m *Model) []string {
	t.Helper()
	if m.msg == "" {
		return nil
	}
	return strings.Split(m.msg, ";;")
}

func containsPrefix(rows []string, prefix string) bool {
	for _, r := range rows {
		if strings.HasPrefix(r, prefix) {
			return true
		}
	}
	return false
}

// TestPluginBusEventsEndToEnd: a buffer edit publishes buffer:modified to the
// bus, a file change publishes file:changed, and after the pump drains the
// plugin's handler has seen all of it — delivered on the editor thread, never
// inline at the point of publication.
func TestPluginBusEventsEndToEnd(t *testing.T) {
	m := pluginEventFixture(t)

	// Nothing published before anything changed.
	m.emitBufferModified()
	m.drainPlugins()
	if rows := seenEvents(t, &m); len(rows) != 0 {
		t.Fatalf("spurious events before any edit: %v", rows)
	}

	// An edit moves the counter; the event goes to the pump, not inline.
	m.tabs[0].buf.InsertText("x")
	m.emitBufferModified()
	m.drainPlugins()
	rows := seenEvents(t, &m)
	if len(rows) != 1 || !containsPrefix(rows, "buffer:modified|") {
		t.Fatalf("rows=%v", rows)
	}

	// No further edits, no further events.
	m.emitBufferModified()
	m.drainPlugins()
	if rows := seenEvents(t, &m); len(rows) != 1 {
		t.Fatalf("re-published without an edit: %v", rows)
	}

	// A file change published on the UI thread (the watcher branch) reaches
	// the handler with the path as the payload.
	m.publishBus(events.Event{Type: events.EventFileChanged, Path: "notes.txt"})
	m.drainPlugins()
	rows = seenEvents(t, &m)
	if !containsPrefix(rows, "file:changed|notes.txt") {
		t.Fatalf("rows=%v", rows)
	}

	// The same event published from the agent-worker side of the bus (what
	// commit.go does on its goroutine).
	m.bus.Publish(events.Event{Type: events.EventAgentUpdated, Path: "task-2"})
	m.drainPlugins()
	rows = seenEvents(t, &m)
	if !containsPrefix(rows, "agent:updated|task-2") {
		t.Fatalf("rows=%v", rows)
	}
}
