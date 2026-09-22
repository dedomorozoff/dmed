package dbgp

import (
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"dmed/internal/dap"
)

// The in-memory fakeEngine mirrors the protocol as we understand it, so it can
// never catch a wrong understanding: the framing and the XML prolog charset it
// used to get wrong both shipped as real bugs. These tests drive a real PHP
// interpreter with Xdebug instead, and skip when either is unavailable.

// xdebugDebugMode reports whether the php binary has Xdebug loaded with step
// debugging enabled — without it the engine never dials back.
func xdebugDebugMode(t *testing.T, php string) bool {
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

// freePort reserves a loopback port for the engine to dial back to. The
// listener is closed before returning, so the port is free for StartDebugger.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	defer ln.Close()
	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
}

// waitEvent returns the first event of the wanted kind, discarding others.
func waitEvent(t *testing.T, ch <-chan dap.Event, want dap.EventKind, d time.Duration) dap.Event {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case e := <-ch:
			if e.Kind == want {
				return e
			}
		case <-deadline:
			t.Fatalf("no %v event within %s", want, d)
			return dap.Event{}
		}
	}
}

func TestRealXdebugSession(t *testing.T) {
	if testing.Short() {
		t.Skip("needs a PHP interpreter")
	}
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not installed")
	}
	if !xdebugDebugMode(t, php) {
		t.Skip("xdebug step debugging is not enabled for this php")
	}

	script, err := filepath.Abs(filepath.Join("testdata", "script.php"))
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan dap.Event, 128)

	var c *Client
	for attempt := 0; ; attempt++ {
		c, err = StartDebugger(php, script, nil, filepath.Dir(script),
			"127.0.0.1:"+freePort(t),
			func(e dap.Event) {
				select {
				case events <- e:
				default:
				}
			})
		if err == nil {
			break
		}
		if attempt == 2 {
			t.Fatalf("StartDebugger: %v", err)
		}
	}
	defer c.Close()

	if _, err := c.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	bps, err := c.SetBreakpoints(script, []int{7})
	if err != nil || len(bps) != 1 || !bps[0].Verified {
		t.Fatalf("SetBreakpoints = %+v, %v", bps, err)
	}

	if err := c.ConfigureDone(); err != nil {
		t.Fatalf("ConfigureDone: %v", err)
	}
	stop := waitEvent(t, events, dap.EventStopped, 15*time.Second)
	if !strings.EqualFold(filepath.Clean(stop.SourcePath), filepath.Clean(script)) {
		t.Errorf("stop path = %q, want %q", stop.SourcePath, script)
	}
	if stop.Line != 7 {
		t.Fatalf("stop line = %d, want 7", stop.Line)
	}

	th, err := c.Threads()
	if err != nil || len(th) == 0 {
		t.Fatalf("Threads = %v, %v", th, err)
	}
	// add() is frame 0, the top-level script is frame 1.
	frames, err := c.StackTrace(th[0].ID, 0)
	if err != nil || len(frames) < 2 {
		t.Fatalf("StackTrace = %v, %v", frames, err)
	}
	scopes, err := c.Scopes(frames[0].ID)
	if err != nil || len(scopes) == 0 {
		t.Fatalf("Scopes = %v, %v", scopes, err)
	}
	vars, err := c.Variables(scopes[0].VariablesRef)
	if err != nil {
		t.Fatalf("Variables: %v", err)
	}
	got := map[string]string{}
	for _, v := range vars {
		got[strings.TrimPrefix(v.Name, "$")] = v.Value
	}
	if got["a"] != "2" || got["b"] != "3" {
		t.Errorf("locals = %v, want a=2 b=3", got)
	}

	// The expression is base64-encoded on the wire; a verbatim one is decoded
	// as garbage by the engine.
	ev, err := c.Evaluate("$a + $b", frames[0].ID)
	if err != nil || ev.Value != "5" {
		t.Errorf("Evaluate = %+v, %v", ev, err)
	}

	// The array lives in the caller's frame; expanding it walks the same
	// scope -> variable -> property_get path the variables panel uses.
	mainScopes, err := c.Scopes(frames[1].ID)
	if err != nil || len(mainScopes) == 0 {
		t.Fatalf("caller Scopes = %v, %v", mainScopes, err)
	}
	mainVars, err := c.Variables(mainScopes[0].VariablesRef)
	if err != nil {
		t.Fatalf("caller Variables: %v", err)
	}
	var itemsRef int64
	for _, v := range mainVars {
		if strings.TrimPrefix(v.Name, "$") == "items" {
			itemsRef = v.VariablesRef
			if v.Value != "array(2)" {
				t.Errorf("$items = %q, want array(2)", v.Value)
			}
		}
	}
	if itemsRef == 0 {
		t.Fatalf("$items is not expandable: %+v", mainVars)
	}
	elems, err := c.Variables(itemsRef)
	if err != nil {
		t.Fatalf("array elements: %v", err)
	}
	if len(elems) != 2 || elems[0].Value != "alpha" || elems[1].Value != "beta" {
		t.Errorf("elements = %+v", elems)
	}

	if err := c.StepIn(th[0].ID); err != nil {
		t.Fatalf("StepIn: %v", err)
	}
	if s := waitEvent(t, events, dap.EventStopped, 15*time.Second); s.Line != 8 {
		t.Errorf("step stop line = %d, want 8", s.Line)
	}

	if err := c.Continue(th[0].ID); err != nil {
		t.Fatalf("Continue: %v", err)
	}
	// The script prints, exits, and Xdebug drops the socket: a clean end of
	// session must terminate the panel, not look like a lost connection.
	waitEvent(t, events, dap.EventExited, 15*time.Second)
	waitEvent(t, events, dap.EventTerminated, 15*time.Second)
}
