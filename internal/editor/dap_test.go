package editor

import (
	"io"
	"testing"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/dap"
)

// nopConn is a sink/source conn: writes are accepted instantly and reads hit
// EOF so the client's read loop exits immediately.
type nopConn struct{}

func (nopConn) Read([]byte) (int, error)    { return 0, io.EOF }
func (nopConn) Write(p []byte) (int, error) { return len(p), nil }
func (nopConn) Close() error                { return nil }

// newStubDAPClient returns a real Client over a no-op stream; enough for code
// paths that only touch the handle/Close.
func newStubDAPClient() *dap.Client {
	return dap.NewClient(nopConn{}, func(dap.Event) {})
}

func TestDapAdapterCommand(t *testing.T) {
	// Default dlv gets the `dap` subcommand prepended.
	bin, argv := dapAdapterCommand("dlv", "")
	if bin != "dlv" || len(argv) != 1 || argv[0] != "dap" {
		t.Fatalf("dlv default = %s %v, want [dap]", bin, argv)
	}
	// Explicit args keep dap first.
	_, argv = dapAdapterCommand("dlv.exe", "--log-dir /tmp/dap")
	if len(argv) != 3 || argv[0] != "dap" || argv[1] != "--log-dir" {
		t.Fatalf("dlv with args = %v, want [dap --log-dir /tmp/dap]", argv)
	}
	// A pre-supplied dap subcommand is not duplicated.
	_, argv = dapAdapterCommand("dlv", "dap --log-dir /tmp/dap")
	if len(argv) != 3 || argv[0] != "dap" {
		t.Fatalf("dlv dap ... = %v, want [dap --log-dir /tmp/dap]", argv)
	}
	// Generic adapters pass args through untouched.
	_, argv = dapAdapterCommand("debugpy-adapter", "--log-dir /tmp/x")
	if len(argv) != 2 || argv[0] != "--log-dir" {
		t.Fatalf("debugpy args = %v, want passthrough", argv)
	}
}

func TestDapLaunchArgs(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24

	// Defaults compose the Delve launch shape.
	args, err := m.dapLaunchArgs()
	if err != nil {
		t.Fatal(err)
	}
	if args["type"] != "go" || args["request"] != "launch" {
		t.Fatalf("defaults = %+v, want type=go request=launch", args)
	}
	if args["mode"] != "debug" {
		t.Fatalf("mode = %v, want debug", args["mode"])
	}
	if _, ok := args["stopOnEntry"]; ok {
		t.Fatalf("stopOnEntry must be omitted by default, got %v", args["stopOnEntry"])
	}

	// Config values flow through.
	m.cfg.Debug.Mode = "test"
	m.cfg.Debug.Program = "./pkg"
	m.cfg.Debug.Args = "-run TestFoo"
	m.cfg.Debug.StopOnEntry = true
	args, err = m.dapLaunchArgs()
	if err != nil {
		t.Fatal(err)
	}
	if args["mode"] != "test" || args["program"] != "./pkg" {
		t.Fatalf("args = %+v", args)
	}
	if ag := args["args"].([]string); len(ag) != 2 || ag[0] != "-run" || ag[1] != "TestFoo" {
		t.Fatalf("args slice = %+v", args["args"])
	}
	if args["stopOnEntry"] != true {
		t.Fatalf("stopOnEntry = %v, want true", args["stopOnEntry"])
	}

	// Adapter-specific launch_json overrides built-in keys.
	m.cfg.Debug.LaunchJSON = `{"type": "python", "justMyCode": false}`
	args, err = m.dapLaunchArgs()
	if err != nil {
		t.Fatal(err)
	}
	if args["type"] != "python" || args["justMyCode"] != false {
		t.Fatalf("launch_json override = %+v", args)
	}

	// Broken launch_json is reported, not swallowed.
	m.cfg.Debug.LaunchJSON = "{oops"
	if _, err := m.dapLaunchArgs(); err == nil {
		t.Fatal("bad launch_json must error")
	}
}

func TestDapPanelInputFocus(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.dapOpen = true

	// Tab cycles threads -> stack -> variables -> input row.
	for want := 1; want <= 3; want++ {
		m = press(m, tea.KeyPressMsg{Code: tea.KeyTab})
		if m.dapFocus != want {
			t.Fatalf("focus after %d tabs = %d, want %d", want, m.dapFocus, want)
		}
	}
	// Tab again wraps back to threads.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.dapFocus != 0 {
		t.Fatalf("focus wrapped = %d, want 0", m.dapFocus)
	}

	// A plain character jumps straight to the input row.
	m = press(m, tea.KeyPressMsg{Text: "p"})
	if m.dapFocus != 3 {
		t.Fatalf("typing must focus input, focus = %d", m.dapFocus)
	}
	// Navigation letters are typed literally while the input row is focused.
	m = press(m, tea.KeyPressMsg{Text: "r"})
	m = press(m, tea.KeyPressMsg{Text: "l"})
	m = press(m, tea.KeyPressMsg{Text: "j"})
	if got := string(m.dapIn); got != "prlj" {
		t.Fatalf("input = %q, want prlj", got)
	}

	// Enter evaluates (no live session -> no-op) and clears the buffer.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.dapIn) != 0 {
		t.Fatalf("enter must clear input, got %q", string(m.dapIn))
	}
	if m.dapFocus != 3 {
		t.Fatalf("focus after eval = %d, want 3", m.dapFocus)
	}

	// Esc clears a pending expression first, then leaves the input row.
	m = press(m, tea.KeyPressMsg{Text: "x"})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if len(m.dapIn) != 0 {
		t.Fatalf("esc must clear input, got %q", string(m.dapIn))
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.dapFocus != 0 || !m.dapOpen {
		t.Fatalf("second esc must return to lists and keep panel open, focus=%d open=%v", m.dapFocus, m.dapOpen)
	}

	// In list focus, l toggles the console peek instead of typing.
	if m.dapConsolePeek {
		t.Fatal("console peek must start off")
	}
	m = press(m, tea.KeyPressMsg{Text: "l"})
	if !m.dapConsolePeek {
		t.Fatal("l in list focus must toggle console peek")
	}
	if len(m.dapIn) != 0 {
		t.Fatalf("l in list focus must not type, got %q", string(m.dapIn))
	}
}

func TestDapRestartAfterEnded(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.dapOpen = true
	// A non-existent adapter makes the async start fail fast (LookPath), so
	// running the returned command cannot spawn a real debugger.
	m.cfg.Debug.AdapterCmd = "dmed-test-no-such-adapter"

	// While a session is running, F5 is a no-op.
	m.dapRunState = dapRunning
	m.dapClient = newStubDAPClient()
	if cmd := m.startDebugging(); cmd != nil {
		m.dapClient.Close()
		t.Fatal("startDebugging must be a no-op while running")
	}
	m.dapClient.Close()

	// After the process exited, F5 tears the ended session down and relaunches.
	m.dapRunState = dapEnded
	m.dapClient = newStubDAPClient()
	cmd := m.startDebugging()
	if cmd == nil {
		t.Fatal("startDebugging must relaunch after an ended session")
	}
	if m.dapRunState != dapLaunch || !m.dapBusy {
		t.Fatalf("state = %q busy=%v, want launch busy=true", m.dapRunState, m.dapBusy)
	}
	if m.dapClient != nil {
		t.Fatal("ended client must be dropped from the model")
	}
	_ = cmd() // closes the stub; the adapter start fails fast on LookPath
}
