package editor

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// TestDlvEndToEnd drives the full design-time flow against a real Delve
// adapter: reverse-connect spawn, initialize handshake, launch, and the
// event stream all the way to process exit + session termination. It proves
// a default `dlv dap` invocation actually starts a debug session — the
// "silently nothing happens" symptom.
func TestDlvEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("dlv"); err != nil {
		t.Skip("no dlv on PATH")
	}

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "go.mod"), "module demo\n\ngo 1.26\n")
	mustWrite(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n")

	m := New()
	m.width, m.height = 80, 24
	m.cfg.Debug.Program = dir

	runDlvSessionToEnd(t, &m)
}

// TestDlvSingleFileNoModule reproduces the user's setup: the debug folder has
// a lone .go file and no go.mod. dapProgram must fall back to launching the
// file itself (delve builds `go build <file>.go`, which needs no module) so
// the session still runs to completion instead of failing to build.
func TestDlvSingleFileNoModule(t *testing.T) {
	if _, err := exec.LookPath("dlv"); err != nil {
		t.Skip("no dlv on PATH")
	}

	dir := t.TempDir()
	mainGo := filepath.Join(dir, "main.go")
	mustWrite(t, mainGo, "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n")
	for _, name := range []string{"go.mod", "go.work"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Fatalf("%s must not exist: this test needs a module-less dir", name)
		}
	}

	m := New(mainGo) // active tab is the lone Go file
	m.width, m.height = 80, 24

	// The module-less dir now resolves to the file itself.
	if p := m.dapProgram(); p != mainGo {
		t.Fatalf("dapProgram = %q, want the lone file %q", p, mainGo)
	}

	runDlvSessionToEnd(t, &m)
}

// runDlvSessionToEnd starts debugging on m, pumps messages/events until the
// session reaches dapEnded, verifies the bookkeeping, then tears it down.
func runDlvSessionToEnd(t *testing.T, m *Model) {
	t.Helper()

	start := m.startDebugging()
	if start == nil {
		t.Fatal("startDebugging returned no cmd")
	}

	msgs := make(chan tea.Msg, 256)
	run := func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() { msgs <- c() }()
	}
	run(start)

	deadline := time.After(30 * time.Second)
	for {
		select {
		case msg := <-msgs:
			if drainDlvBatch(msg, run) {
				continue
			}
			nm, extra := m.Update(msg)
			*m = nm.(Model)
			run(extra)
		case evm := <-m.dapCh:
			nm, _ := m.Update(evm)
			*m = nm.(Model)
		case <-deadline:
			t.Fatalf("timed out; state=%q msg=%q console=%q", m.dapRunState, m.msg, m.dapConsole)
		}
		if m.dapRunState == dapEnded && stringHasPrefix(m.dapConsole, "process exited") {
			break
		}
	}

	if m.msg != "debug: session ended" {
		t.Errorf("final status = %q, want session ended note", m.msg)
	}
	if m.dapClient == nil {
		t.Error("session client must still be attached after termination")
	}
	if m.dapConsole == nil || len(m.dapConsole) == 0 {
		t.Error("console must hold output from the run")
	}
	if len(m.dapConsole) > 0 && m.dapConsole[len(m.dapConsole)-1] != "debug session terminated" {
		t.Errorf("console must end with termination note, got %q", m.dapConsole[len(m.dapConsole)-1])
	}
	stop := m.stopDebugging()
	if stop != nil {
		done := make(chan tea.Msg, 1)
		go func() { done <- stop() }()
		<-done // client Close sent; let the adapter notice disconnect and exit
	}
	time.Sleep(2 * time.Second)
}

// TestDlvBreakpointHit is the regression for "breakpoints do nothing and the
// debugger hangs": it drives a real Delve session with a breakpoint set
// through the editor's own toggle and requires the adapter to actually stop on
// that line, then run to completion. The launch sequence used to push
// setBreakpoints before launch, which Delve rejects with "No debug session
// started" — so the debuggee never started, the panel stayed empty and every
// later F5 was swallowed by the "session already attached" guard.
func TestDlvBreakpointHit(t *testing.T) {
	if _, err := exec.LookPath("dlv"); err != nil {
		t.Skip("no dlv on PATH")
	}

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "go.mod"), "module demo\n\ngo 1.26\n")
	mainGo := filepath.Join(dir, "main.go")
	mustWrite(t, mainGo, "package main\n\nfunc main() {\n\tsum := 0\n\tfor i := 0; i < 3; i++ {\n\t\tsum += i\n\t\tprintln(sum)\n\t}\n\t_ = sum\n}\n")

	m := New(mainGo)
	m.width, m.height = 80, 24
	// "sum += i" is line 6; the loop hits it three times.
	m.toggleBreakpointAt(5)
	abs, _ := filepath.Abs(mainGo)
	if !m.dapBreak[abs][6] {
		t.Fatalf("breakpoint must be recorded for %s, got %v", abs, m.dapBreak)
	}

	stopLine := 0
	resumed := false
	pumpDlvSession(t, &m, func(m *Model) tea.Cmd {
		// Resume only once the stack snapshot of this stop has arrived, so the
		// stop line can be inspected before the debuggee moves on.
		if m.dapRunState == dapStopped && len(m.dapFrames) > 0 {
			if resumed {
				return nil
			}
			resumed = true
			return m.startDebugging() // F5 resumes from a stop
		}
		if m.dapRunState == dapRunning {
			resumed = false
		}
		return nil
	}, func(m *Model) bool {
		if m.dapRunState == dapStopped && len(m.dapFrames) > 0 && stopLine == 0 {
			stopLine = m.dapFrames[0].Line
		}
		return m.dapRunState == dapEnded
	})
	if stopLine == 0 {
		t.Fatal("the breakpoint never stopped the debuggee")
	}
	if stopLine != 6 {
		t.Errorf("stopped at line %d, want 6", stopLine)
	}
	if v, ok := m.dapBPVerif[abs][6]; !ok || !v {
		t.Errorf("verification for %s:6 = %v (present=%v), want true", abs, v, ok)
	}
	stopDlvSession(t, &m)
}

// TestDlvPauseRunningDebuggee proves F5 can interrupt a run. A debuggee that
// never reaches a breakpoint (here it loops forever) used to leave F5 a dead
// key with no way to pause it.
func TestDlvPauseRunningDebuggee(t *testing.T) {
	if _, err := exec.LookPath("dlv"); err != nil {
		t.Skip("no dlv on PATH")
	}

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "go.mod"), "module demo\n\ngo 1.26\n")
	mainGo := filepath.Join(dir, "main.go")
	mustWrite(t, mainGo, "package main\n\nimport \"time\"\n\nfunc main() {\n\tfor {\n\t\ttime.Sleep(10 * time.Millisecond)\n\t}\n}\n")

	m := New(mainGo)
	m.width, m.height = 80, 24

	paused := false
	pumpDlvSession(t, &m, func(m *Model) tea.Cmd {
		if m.dapRunState == dapRunning && !paused {
			paused = true
			return m.startDebugging() // F5 while running interrupts
		}
		return nil
	}, func(m *Model) bool {
		return paused && m.dapRunState == dapStopped
	})
	if m.dapRunState != dapStopped {
		t.Fatalf("state = %q, want stopped after pause", m.dapRunState)
	}
	if m.dapReason != "pause" {
		t.Errorf("stop reason = %q, want pause", m.dapReason)
	}
	stopDlvSession(t, &m)
}

// stopDlvSession tears the session down and gives the adapter a moment to kill
// the debuggee, so the temp directory can be removed afterwards.
func stopDlvSession(t *testing.T, m *Model) {
	t.Helper()
	if stop := m.stopDebugging(); stop != nil {
		done := make(chan tea.Msg, 1)
		go func() { done <- stop() }()
		<-done
	}
	time.Sleep(2 * time.Second)
}

// drainDlvBatch unwraps a batched command the way the bubbletea runtime does.
// The inner commands must run: a refresh triggered by an event is delivered
// through tea.Batch, and without this the harness would silently drop it.
func drainDlvBatch(msg tea.Msg, run func(tea.Cmd)) bool {
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return false
	}
	for _, c := range batch {
		run(c)
	}
	return true
}

// pumpDlvSession drives a debug session on m until done reports completion. It
// pumps messages, DAP events and commands; act runs after every step and may
// issue a follow-up command (continue, pause, ...). Because act runs on every
// iteration, it must be idempotent for a given state.
func pumpDlvSession(t *testing.T, m *Model, act func(*Model) tea.Cmd, done func(*Model) bool) {
	t.Helper()
	msgs := make(chan tea.Msg, 256)
	run := func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() { msgs <- c() }()
	}
	run(m.startDebugging())

	deadline := time.After(45 * time.Second)
	for {
		select {
		case msg := <-msgs:
			if drainDlvBatch(msg, run) {
				continue
			}
			nm, extra := m.Update(msg)
			*m = nm.(Model)
			run(extra)
		case evm := <-m.dapCh:
			nm, extra := m.Update(evm)
			*m = nm.(Model)
			run(extra)
		case <-deadline:
			t.Fatalf("timed out; state=%q msg=%q console=%q", m.dapRunState, m.msg, m.dapConsole)
		}
		if act != nil {
			run(act(m))
		}
		if done(m) {
			return
		}
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func stringHasPrefix(ss []string, sub string) bool {
	for _, s := range ss {
		if len(s) >= len(sub) && s[:len(sub)] == sub {
			return true
		}
	}
	return false
}