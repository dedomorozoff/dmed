package editor

import (
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// phpXdebugDebugMode reports whether the php interpreter has Xdebug loaded with
// step debugging enabled; without it the engine never dials back.
func phpXdebugDebugMode(t *testing.T, php string) bool {
	t.Helper()
	out, err := exec.Command(php, "-r", "echo ini_get('xdebug.mode');").Output()
	if err != nil {
		return false
	}
	for _, m := range strings.Split(string(out), ",") {
		if strings.TrimSpace(m) == "debug" {
			return true
		}
	}
	return false
}

// editorFreePort reserves a loopback port for Xdebug to dial back to. The
// listener is closed before returning, so the port is free for the session.
func editorFreePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	defer ln.Close()
	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
}

// TestPHPXdebugBreakpointHit is the PHP counterpart of TestDlvBreakpointHit: it
// drives a real PHP interpreter with Xdebug through the editor's own flow — the
// language preset picks the DBGp transport, the gutter breakpoint goes over the
// wire, and the session must actually stop on it before running to completion.
// The DBGp client is a second protocol implementation behind the same panel, so
// it needs the same "does anything happen at all" guard the Delve tests give
// DAP.
func TestPHPXdebugBreakpointHit(t *testing.T) {
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("no php on PATH")
	}
	if !phpXdebugDebugMode(t, php) {
		t.Skip("xdebug step debugging is not enabled for this php")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "index.php")
	mustWrite(t, script, "<?php\n$items = [\"alpha\", \"beta\"];\n\necho \"start\\n\";\nforeach ($items as $k => $v) {\n    echo \"$k => $v\\n\";\n}\necho \"done\\n\";\n")

	m := New(script)
	m.width, m.height = 80, 24
	// Pin only the port; the rest stays at its default, so the PHP language
	// preset (dbgp transport, `php` interpreter) is what gets exercised.
	m.cfg.Debug.AdapterArgs = "127.0.0.1:" + editorFreePort(t)

	// "foreach ($items as $k => $v) {" is line 5.
	m.toggleBreakpointAt(4)
	abs, _ := filepath.Abs(script)
	if !m.dapBreak[abs][5] {
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
		if stopLine == 0 && m.dapRunState == dapStopped && len(m.dapFrames) > 0 {
			stopLine = m.dapFrames[0].Line
		}
		return m.dapRunState == dapEnded
	})

	if stopLine == 0 {
		t.Fatalf("the breakpoint never stopped the debuggee; state=%q msg=%q console=%q",
			m.dapRunState, m.msg, m.dapConsole)
	}
	if stopLine != 5 {
		t.Errorf("stopped at line %d, want 5", stopLine)
	}
	if v, ok := m.dapBPVerif[abs][5]; !ok || !v {
		t.Errorf("verification for %s:5 = %v (present=%v), want true", abs, v, ok)
	}
	stopDlvSession(t, &m)
}
