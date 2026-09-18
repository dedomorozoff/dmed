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