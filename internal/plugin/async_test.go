package plugin

import (
	"runtime"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// luaTableRows reads a string-array global the test plugins build.
func luaTableRows(L *lua.LState, name string) []string {
	t, ok := L.GetGlobal("dmed").(*lua.LTable).RawGetString(name).(*lua.LTable)
	if ok == false {
		return nil
	}
	var rows []string
	t.ForEach(func(i lua.LValue, v lua.LValue) {
		if s, ok := v.(lua.LString); ok {
			rows = append(rows, string(s))
		}
	})
	return rows
}

// TestPostEventDeliversOnDrain covers the queue/wake/drain contract: PostEvent
// is safe before any delivery, wakes the host once per job, and Drain
// resolves handlers at delivery time with the payload as the argument.
func TestPostEventDeliversOnDrain(t *testing.T) {
	dir := t.TempDir()
	writePlug(t, dir, "ev.lua", `
dmed.seen = {}
dmed.on("file:changed", function(arg)
  dmed.seen[#dmed.seen+1] = "file:changed " .. (arg or "")
  dmed.status("got event")
end)
`)
	m := New()
	wakes := 0
	m.SetWake(func() { wakes++ })
	if errs := m.Load(dir); len(errs) > 0 {
		t.Fatalf("load errors: %v", errs)
	}
	m.PostEvent("file:changed", "main.go")
	m.PostEvent("file:changed", "other.go")
	m.PostEvent("agent:updated", "task-1")
	if got := m.Pending(); got != 3 {
		t.Fatalf("pending=%d, want 3", got)
	}
	if wakes != 3 {
		t.Fatalf("wake calls=%d, want 3", wakes)
	}
	h := &fakeHost{}
	m.Drain(h)
	if got := m.Pending(); got != 0 {
		t.Fatalf("pending after drain=%d", got)
	}
	if h.status != "got event" {
		t.Fatalf("status=%q", h.status)
	}
	rows := luaTableRows(m.plugins[0].L, "seen")
	if len(rows) != 2 || rows[0] != "file:changed main.go" || rows[1] != "file:changed other.go" {
		t.Fatalf("rows=%v", rows)
	}
}

// TestDrainSkipsClosedPlugin: jobs queued for a plugin that Reload/Remove
// dropped meanwhile must be skipped, not delivered into a closed LState.
func TestDrainSkipsClosedPlugin(t *testing.T) {
	dir := t.TempDir()
	writePlug(t, dir, "gone.lua", `
dmed.on("buffer:modified", function() dmed.status("still here") end)
dmed.spawn("cmd_that_does_not_exist_xyz", {timeout = 1}, function() end)
`)
	m := New()
	if errs := m.Load(dir); len(errs) != 0 {
		t.Fatalf("load errors: %v", errs)
	}
	path := m.plugins[0].path
	m.PostEvent("buffer:modified", "")
	// The spawn of an unknown command fails to start and queues its callback
	// right away; wait for it, then drop the plugin before delivery.
	deadline := time.Now().Add(2 * time.Second)
	for m.Pending() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	m.Remove(path)
	h := &fakeHost{}
	m.Drain(h)
	if h.status == "still here" {
		t.Fatal("handler of a removed plugin ran")
	}
}

// TestDrainDeliversLateRegisteredHandler: PostEvent queues the event only;
// Drain reads the handlers that exist at delivery time, so a handler
// registered after the enqueue still receives it.
func TestDrainDeliversLateRegisteredHandler(t *testing.T) {
	dir := t.TempDir()
	writePlug(t, dir, "late.lua", `
dmed.late = {}
dmed.on("agent:updated", function(arg) dmed.late[#dmed.late+1] = arg end)
`)
	m := New()
	if errs := m.Load(dir); len(errs) > 0 {
		t.Fatalf("load errors: %v", errs)
	}
	m.PostEvent("agent:updated", "task-7")
	if err := m.plugins[0].L.DoString(`
dmed.on("agent:updated", function(arg) dmed.late[#dmed.late+1] = "second "..(arg or "") end)
`); err != nil {
		t.Fatal(err)
	}
	m.Drain(&fakeHost{})
	rows := luaTableRows(m.plugins[0].L, "late")
	if len(rows) != 2 || rows[0] != "task-7" || rows[1] != "second task-7" {
		t.Fatalf("rows=%v", rows)
	}
}

// TestSpawnCompletion: dmed.spawn returns at once, captures the output and
// delivers {code=, out=} through the callback on the next Drain.
func TestSpawnCompletion(t *testing.T) {
	// echo is a cmd.exe builtin on Windows and a real binary elsewhere.
	var spLua string
	if runtime.GOOS == "windows" {
		spLua = `
dmed.exit = 0
dmed.spawn({cmd = "cmd", args = {"/c", "echo", "hello"}}, function(r)
  dmed.exit = (r.code == 0) and (r.out:match("hello") ~= nil) and 1 or 0
end)`
	} else {
		spLua = `
dmed.exit = 0
dmed.spawn({cmd = "echo", args = {"hello"}}, function(r)
  dmed.exit = (r.code == 0) and (r.out:match("hello") ~= nil) and 1 or 0
end)`
	}
	dir := t.TempDir()
	writePlug(t, dir, "sp.lua", spLua)
	m := New()
	if errs := m.Load(dir); len(errs) > 0 {
		t.Fatalf("load errors: %v", errs)
	}
	if m.Pending() != 0 {
		t.Fatalf("spawn must not queue inline, pending=%d", m.Pending())
	}
	waitQueue(t, m, 1)
	L := m.plugins[0].L
	m.Drain(&fakeHost{})
	exit := L.GetGlobal("dmed").(*lua.LTable).RawGetString("exit")
	if exit != lua.LNumber(1) {
		t.Fatalf("callback saw the wrong result: dmed.exit=%v", exit)
	}
}

// TestSpawnTimeout: a process outliving its deadline is killed and the
// callback reports timeout=true.
func TestSpawnTimeout(t *testing.T) {
	var slowLua string
	if runtime.GOOS == "windows" {
		slowLua = `dmed.spawn({cmd = "cmd", args = {"/c", "ping", "-n", "5", "127.0.0.1"}, timeout = 0.3}, function(r) dmed.to = r.timeout and 1 or 0 end)`
	} else {
		slowLua = `dmed.spawn({cmd = "sleep", args = {"5"}, timeout = 0.3}, function(r) dmed.to = r.timeout and 1 or 0 end)`
	}
	dir := t.TempDir()
	writePlug(t, dir, "slow.lua", slowLua)
	m := New()
	if errs := m.Load(dir); len(errs) > 0 {
		t.Fatalf("load errors: %v", errs)
	}
	waitQueue(t, m, 1)
	m.Drain(&fakeHost{})
	to := m.plugins[0].L.GetGlobal("dmed").(*lua.LTable).RawGetString("to")
	if to != lua.LNumber(1) {
		t.Fatalf("expected timeout=true, saw dmed.to=%v", to)
	}
}

// TestPendingEventNotDeliveredWithoutDrain: queueing alone invokes nothing,
// which is what keeps a background goroutine from ever running Lua.
func TestPendingEventNotDeliveredWithoutDrain(t *testing.T) {
	dir := t.TempDir()
	writePlug(t, dir, "q.lua", `
dmed.touch = 0
dmed.on("file:changed", function() dmed.touch = 1 end)
`)
	m := New()
	if errs := m.Load(dir); len(errs) > 0 {
		t.Fatalf("load errors: %v", errs)
	}
	m.PostEvent("file:changed", "")
	touch := m.plugins[0].L.GetGlobal("dmed").(*lua.LTable).RawGetString("touch")
	if touch != lua.LNumber(0) {
		t.Fatalf("handler ran without Drain: touch=%v", touch)
	}
}

// waitQueue polls until pending reaches want (a finished spawn queues its
// callback from its own goroutine).
func waitQueue(t *testing.T, m *Manager, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for m.Pending() < want {
		if time.Now().After(deadline) {
			t.Fatalf("queue never reached %d (at %d)", want, m.Pending())
		}
		time.Sleep(5 * time.Millisecond)
	}
}
