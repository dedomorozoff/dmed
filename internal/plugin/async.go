// Async delivery for the plugin framework.
//
// Two kinds of Lua work cannot run where they originate:
//
//   - Events arrive from the editor's event bus on whatever goroutine
//     published them (an agent runner, the process committer) — and Lua
//     handlers edit buffers, so they must run on the editor's UI thread.
//   - dmed.spawn commands finish on their own goroutine, but the completion
//     callback is Lua and the same rule applies.
//
// Both funnel through one queue: the producer appends a queuedJob (safe from
// any goroutine) and asks the host to wake; the host — the editor's UI thread
// — calls Drain, which is the only point Lua runs after an async enqueue. So
// a plugin never blocks the editor, and the editor never calls Lua from off
// the UI thread.
package plugin

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"

	"dmed/internal/debug"
)

// defaultSpawnTimeout bounds a dmed.spawn subprocess when the plugin did not
// say otherwise. Subprocesses legitimately run longer than the per-call Lua
// budget, but none may run forever: a plugin must be able to give up.
const defaultSpawnTimeout = 10 * time.Second

const (
	minSpawnTimeout = 100 * time.Millisecond
	maxSpawnTimeout = 10 * time.Minute
)

// maxSpawnOutput caps how much of a subprocess's stdout/stderr is captured.
// A command that streams gigabytes delivers its first megabyte, not an OOM.
const maxSpawnOutput = 1 << 20

type jobKind int

const (
	jobEvent jobKind = iota
	jobSpawn
)

// queuedJob is one deferred Lua delivery.
type queuedJob struct {
	kind jobKind
	// jobEvent only.
	event string
	arg   string // payload: a file path, or an agent task id
	// jobSpawn only.
	p   *Plugin
	fn  lua.LValue    // the callback
	res *spawnOutcome // the process result
}

// spawnOutcome is what a finished subprocess reports to its callback.
type spawnOutcome struct {
	code     int    // exit code; -1 when the process never started
	out      string // captured stdout (first maxSpawnOutput bytes)
	errOut   string // captured stderr, or the start error
	timedOut bool   // killed because the deadline expired
}

// spawnSpec is the parsed form of the table (or string) dmed.spawn accepts.
type spawnSpec struct {
	cmd     string
	args    []string
	timeout time.Duration
	cwd     string
}

// SetWake installs the callback the manager calls after every queue append to
// ask the host to drain the queue. A nil wake is legal: a manager with no
// delivery loop (tests) queues silently and delivers on demand.
func (m *Manager) SetWake(fn func()) {
	m.wake = fn
}

func (m *Manager) wakeHost() {
	if w := m.wake; w != nil {
		w()
	}
}

// Pending reports how many jobs wait for delivery. The editor restores its
// pump from it and tests assert on it.
func (m *Manager) Pending() int {
	m.asyncMu.Lock()
	defer m.asyncMu.Unlock()
	return len(m.pending)
}

// queue appends a job and asks the host to wake. Safe from any goroutine.
func (m *Manager) queue(j queuedJob) {
	m.asyncMu.Lock()
	m.pending = append(m.pending, j)
	m.asyncMu.Unlock()
	m.wakeHost()
}

// PostEvent queues event for delivery to plugin handlers on the UI thread.
// It is safe from any goroutine — the event bus publishes from agent
// runners and background passes — and deliberately does not read the plugin
// list, which only the UI thread may touch: Drain resolves the handlers of
// each event at delivery time, so a handler registered by a plugin after the
// event was queued still receives it if it is still queued.
func (m *Manager) PostEvent(event, arg string) {
	m.queue(queuedJob{kind: jobEvent, event: event, arg: arg})
}

// Drain delivers every queued job on the caller's goroutine — for the editor
// that is the UI thread, the only goroutine a plugin's LState is safe on.
// Handlers and callbacks are resolved now, not at enqueue time, so a plugin
// suppressed by a closed LState (hot-reload removing the old instance,
// uninstall) is skipped instead of panicking.
func (m *Manager) Drain(host Host) {
	m.asyncMu.Lock()
	jobs := m.pending
	m.pending = nil
	m.asyncMu.Unlock()
	for _, j := range jobs {
		switch j.kind {
		case jobSpawn:
			if j.p.closed || j.res == nil {
				continue
			}
			t := j.p.L.NewTable()
			t.RawSetString("code", lua.LNumber(j.res.code))
			t.RawSetString("out", lua.LString(j.res.out))
			t.RawSetString("err", lua.LString(j.res.errOut))
			t.RawSetString("timeout", lua.LBool(j.res.timedOut))
			m.invokeWith(j.p, host, j.fn, []lua.LValue{t})
		case jobEvent:
			for _, p := range m.plugins {
				if p.closed {
					continue
				}
				for _, fn := range p.eventHandlers[j.event] {
					if j.arg == "" {
						m.invokeWith(p, host, fn, nil)
					} else {
						m.invokeWith(p, host, fn, []lua.LValue{lua.LString(j.arg)})
					}
				}
			}
		}
	}
}

// startSpawn parses the spec, spending the process budget, and runs the
// subprocess on its own goroutine. The callback is never called inline: the
// result lands in the queue for the UI thread, which is what makes spawn
// asynchronous for the caller instead of a machine-code blocking call.
func (m *Manager) startSpawn(p *Plugin, L *lua.LState, spec lua.LValue, callback lua.LValue) {
	ss, perr := parseSpawnSpec(L, spec)
	if perr != "" {
		L.RaiseError("spawn: %s", perr)
		return
	}
	go debug.CapturePanicReport(func() {
		m.queue(queuedJob{kind: jobSpawn, p: p, fn: callback, res: runProcess(ss)})
	})
}

// parseSpawnSpec accepts either a command string (split on whitespace, no
// shell — a path with spaces needs the table form) or a table
// {cmd, args, timeout, cwd}, where timeout is seconds of wall clock.
func parseSpawnSpec(L *lua.LState, v lua.LValue) (spawnSpec, string) {
	zero := spawnSpec{}
	switch s := v.(type) {
	case lua.LString:
		fields := strings.Fields(string(s))
		if len(fields) == 0 {
			return zero, "empty command"
		}
		return spawnSpec{cmd: fields[0], args: fields[1:], timeout: defaultSpawnTimeout}, ""
	case *lua.LTable:
		cmd := s.RawGetString("cmd")
		if cmd == lua.LNil {
			return zero, "spec table needs cmd"
		}
		if _, ok := cmd.(lua.LString); !ok {
			return zero, "cmd must be a string"
		}
		ss := spawnSpec{cmd: string(cmd.(lua.LString)), timeout: defaultSpawnTimeout}
		if cwd := s.RawGetString("cwd"); cwd != lua.LNil {
			cwdStr, ok := cwd.(lua.LString)
			if !ok {
				return zero, "cwd must be a string"
			}
			ss.cwd = string(cwdStr)
		}
		if args := s.RawGetString("args"); args != lua.LNil {
			at, ok := args.(*lua.LTable)
			if !ok {
				return zero, "args must be a table of strings"
			}
			at.ForEach(func(i lua.LValue, av lua.LValue) {
				if as, ok := av.(lua.LString); ok {
					ss.args = append(ss.args, string(as))
				}
			})
		}
		if t := s.RawGetString("timeout"); t != lua.LNil {
			tn, ok := t.(lua.LNumber)
			if !ok {
				return zero, "timeout must be seconds (a number)"
			}
			ss.timeout = clampSpawnTimeout(time.Duration(float64(tn) * float64(time.Second)))
		}
		return ss, ""
	default:
		return zero, "spec must be a command string or a table"
	}
}

func clampSpawnTimeout(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultSpawnTimeout
	}
	if d < minSpawnTimeout {
		return minSpawnTimeout
	}
	if d > maxSpawnTimeout {
		return maxSpawnTimeout
	}
	return d
}

// runProcess runs the subprocess under its deadline and captures its output.
// CommandContext kills the child when the deadline expires; the captured
// streams are what the deadline left in the buffers, so a timeout delivers a
// partial result rather than nothing.
func runProcess(ss spawnSpec) *spawnOutcome {
	ctx, cancel := context.WithTimeout(context.Background(), ss.timeout)
	defer cancel()
	res := &spawnOutcome{code: -1}
	cmd := exec.CommandContext(ctx, ss.cmd, ss.args...)
	cmd.Dir = ss.cwd
	out := &limitedBuffer{cap: maxSpawnOutput}
	errOut := &limitedBuffer{cap: maxSpawnOutput}
	cmd.Stdout, cmd.Stderr = out, errOut
	if err := cmd.Start(); err != nil {
		res.errOut = err.Error()
		return res
	}
	waitErr := cmd.Wait()
	res.out, res.errOut = out.String(), errOut.String()
	if state := cmd.ProcessState; state != nil {
		res.code = state.ExitCode()
	}
	res.timedOut = waitErr != nil && ctx.Err() == context.DeadlineExceeded
	return res
}

// limitedBuffer is an io.Writer that keeps at most cap bytes and discards the
// rest, so a run-away producer cannot grow memory without bound.
type limitedBuffer struct {
	buf bytes.Buffer
	cap int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	room := b.cap - b.buf.Len()
	if room > 0 {
		if room > len(p) {
			room = len(p)
		}
		b.buf.Write(p[:room])
	}
	return n, nil // pretend everything arrived: the child must not notice the cap
}

func (b *limitedBuffer) String() string { return b.buf.String() }

// Format is only for test failure messages.
func (r *spawnOutcome) String() string {
	return fmt.Sprintf("code=%d out=%q err=%q timeout=%v", r.code, r.out, r.errOut, r.timedOut)
}
