package editor

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
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

	// Typed characters are ignored by the panel lists: the eval row is opt-in
	// via Tab, so ordinary editing keys are never hijacked by the panel.
	m = press(m, tea.KeyPressMsg{Text: "p"})
	if m.dapFocus != 0 || len(m.dapIn) != 0 {
		t.Fatalf("typing must not reach the panel, focus = %d input = %q", m.dapFocus, string(m.dapIn))
	}

	// Focus the input row explicitly: threads -> stack -> variables -> input.
	for want := 1; want <= 3; want++ {
		m = press(m, tea.KeyPressMsg{Code: tea.KeyTab})
		if m.dapFocus != want {
			t.Fatalf("focus after %d tabs = %d, want %d", want, m.dapFocus, want)
		}
	}
	// Navigation letters are typed literally while the input row is focused.
	m = press(m, tea.KeyPressMsg{Text: "r"})
	m = press(m, tea.KeyPressMsg{Text: "l"})
	m = press(m, tea.KeyPressMsg{Text: "j"})
	if got := string(m.dapIn); got != "rlj" {
		t.Fatalf("input = %q, want rlj", got)
	}

	// Enter evaluates (no live session -> no-op) and clears the buffer.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.dapIn) != 0 {
		t.Fatalf("enter must clear input, got %q", string(m.dapIn))
	}
	if m.dapFocus != 3 {
		t.Fatalf("focus after eval = %d, want 3", m.dapFocus)
	}

	// Esc closes the panel even while the input row is focused.
	m = press(m, tea.KeyPressMsg{Text: "x"})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.dapOpen || len(m.dapIn) != 0 {
		t.Fatalf("esc must close the panel and clear input, open=%v input=%q", m.dapOpen, string(m.dapIn))
	}
	m.dapOpen = true

	// While unfocused and empty, the eval row is hidden: the panel must not
	// look like a command prompt waiting for input.
	m.dapFocus = 0
	m.dapIn = nil
	v := m.View()
	if strings.Contains(v.Content, ">>") {
		t.Fatalf("unfocused empty panel must not show the eval prompt:\n%s", v.Content)
	}
	if !strings.Contains(v.Content, "threads") {
		t.Fatalf("panel must still render its columns:\n%s", v.Content)
	}
	// Focusing the input row (Tab) shows it again.
	m.dapFocus = 3
	v = m.View()
	if !strings.Contains(v.Content, ">>") {
		t.Fatalf("focused input row must render the eval prompt:\n%s", v.Content)
	}
	m.dapFocus = 0

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

func TestDapStepKeys(t *testing.T) {
	f := writeTemp(t, t.TempDir(), "s.txt", "one\n")
	m := New(f)
	m.width, m.height = 80, 24
	m.dapOpen = true
	m.dapRunState = dapStopped
	m.dapClient = newStubDAPClient()
	defer m.dapClient.Close()

	// With the panel open and stopped, F6/F7/S+F7 step and never toggle a
	// split, even though F6/F7 carry the split bindings when the panel is off.
	for _, tc := range []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{"f6 steps over", tea.KeyPressMsg{Code: tea.KeyF6}},
		{"f7 steps in", tea.KeyPressMsg{Code: tea.KeyF7}},
		{"shift+f7 steps out", tea.KeyPressMsg{Code: tea.KeyF7, Mod: tea.ModShift}},
	} {
		next, cmd := m.Update(tc.key)
		m = next.(Model)
		if cmd == nil {
			t.Fatalf("%s: step key must issue a step while stopped", tc.name)
		}
		if m.layout != splitNone {
			t.Fatalf("%s: step key must not toggle a split", tc.name)
		}
		// The command is not run: it would block on a stub client that never
		// answers DAP requests.
	}

	// While running, the step keys are swallowed (no step, no split).
	m.dapRunState = dapRunning
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyF6})
	m = next.(Model)
	if cmd != nil || m.layout != splitNone {
		t.Fatal("F6 while running must be swallowed, not toggle a split")
	}

	// With the panel closed F6 keeps its split binding.
	m.dapOpen = false
	m.dapRunState = dapStopped
	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyF6})
	m = next.(Model)
	if cmd != nil || m.layout != splitVert {
		t.Fatalf("with the panel closed F6 must toggle the split, layout=%d", m.layout)
	}
}

// recConn is a DAP transport that records the request commands the client
// writes and answers each one immediately, so a test can assert the exact
// request sequence without spawning a real adapter.
type recConn struct {
	pr   *io.PipeReader
	pw   *io.PipeWriter
	mu   sync.Mutex
	reqs []string
}

func newRecConn() *recConn {
	pr, pw := io.Pipe()
	return &recConn{pr: pr, pw: pw}
}

func (c *recConn) Read(p []byte) (int, error) { return c.pr.Read(p) }

func (c *recConn) Write(p []byte) (int, error) {
	body := p
	if i := strings.Index(string(p), "\r\n\r\n"); i >= 0 {
		body = p[i+4:]
	}
	var req struct {
		Seq     int64  `json:"seq"`
		Command string `json:"command"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return len(p), nil
	}
	c.mu.Lock()
	c.reqs = append(c.reqs, req.Command)
	c.mu.Unlock()

	res, _ := json.Marshal(map[string]interface{}{
		"type":        "response",
		"request_seq": req.Seq,
		"command":     req.Command,
		"success":     true,
	})
	if _, err := c.pw.Write([]byte(fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(res), res))); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *recConn) Close() error {
	_ = c.pw.Close()
	return c.pr.Close()
}

func (c *recConn) commands() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.reqs...)
}

// TestDapLaunchRequestsBreakpointsAfterLaunch pins the request order of the
// launch sequence. Delve refuses setBreakpoints before launch ("No debug
// session started"), and the old order aborted the launch there — the
// debuggee never started, breakpoints never hit, and F5 stayed dead. Source
// breakpoints must land after launch and before configurationDone.
func TestDapLaunchRequestsBreakpointsAfterLaunch(t *testing.T) {
	conn := newRecConn()
	m := New()
	m.width, m.height = 80, 24
	m.dapClient = dap.NewClient(conn, func(dap.Event) {})
	defer m.dapClient.Close()
	m.dapSupportsConfigDone = true
	m.dapBreak["/tmp/main.go"] = map[int]bool{6: true}

	msg, ok := m.dapLaunchCmd()().(dapLaunchMsg)
	if !ok {
		t.Fatal("launch sequence must report a dapLaunchMsg")
	}
	if msg.err != nil {
		t.Fatalf("launch sequence failed: %v", msg.err)
	}
	want := []string{"launch", "setBreakpoints", "configurationDone"}
	got := conn.commands()
	if len(got) != len(want) {
		t.Fatalf("requests = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("requests = %v, want %v", got, want)
		}
	}
}

// TestDapFailedLaunchReleasesSession is the regression for "after pressing run
// nothing can be launched again": a failed launch used to leave the client
// attached while the state read idle, so the "session already attached" guard
// swallowed every later F5 press.
func TestDapFailedLaunchReleasesSession(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.dapOpen = true
	m.dapClient = newStubDAPClient()
	m.dapRunState = dapLaunch
	m.dapBusy = true
	m.dapGen = 7

	next, cmd := m.Update(dapLaunchMsg{gen: 7, err: errors.New("boom")})
	m = next.(Model)
	if m.dapClient != nil {
		t.Fatal("a failed launch must release the adapter")
	}
	if m.dapRunState != dapIdle || m.dapBusy {
		t.Fatalf("state = %q busy=%v, want idle/false", m.dapRunState, m.dapBusy)
	}
	if !strings.Contains(m.msg, "boom") {
		t.Fatalf("status must report the failure, got %q", m.msg)
	}
	if cmd == nil {
		t.Fatal("releasing a failed session must return a close command")
	}
	_ = cmd()

	// F5 must be able to start over.
	m.cfg.Debug.AdapterCmd = "dmed-test-no-such-adapter"
	relaunch := m.startDebugging()
	if relaunch == nil {
		t.Fatal("F5 must launch again after a failed launch")
	}
	_ = relaunch()
}

// TestDapPanelMouseSelects pins the debug panel hit-testing: a click in the
// columns selects a thread/frame/variable exactly like the arrow keys (the
// panel used to ignore the mouse entirely), and the wheel walks the selection.
func TestDapPanelMouseSelects(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.dapOpen = true
	m.dapFocus = 0
	m.dapThreads = []dap.Thread{{ID: 1, Name: "main"}, {ID: 2, Name: "worker"}}
	m.dapFrames = []dap.StackFrame{
		{ID: 10, Name: "main", Path: "a.go", Line: 3},
		{ID: 11, Name: "helper", Path: "a.go", Line: 9},
	}
	m.dapVarStack = [][]dapVarRow{{{isScope: true, name: "Locals"}, {name: "i", val: "2"}, {name: "s", val: "{…}", ref: 5}}}

	top := m.dapPanelStartRow()
	threadW, frameW, _ := m.dapColumnWidths(m.width)

	// The second screen row of a list is its second entry.
	_ = m.handleMouseClick(tea.MouseClickMsg{X: 2, Y: top + 2})
	if m.dapSelThread != 1 {
		t.Fatalf("thread click: sel = %d, want 1", m.dapSelThread)
	}
	_ = m.handleMouseClick(tea.MouseClickMsg{X: threadW + 2, Y: top + 2})
	if m.dapSelFrame != 1 {
		t.Fatalf("frame click: sel = %d, want 1", m.dapSelFrame)
	}
	_ = m.handleMouseClick(tea.MouseClickMsg{X: threadW + frameW + 3, Y: top + 3})
	if m.dapVarSel != 2 || m.dapFocus != 2 {
		t.Fatalf("variable click: sel = %d focus = %d, want 2/2", m.dapVarSel, m.dapFocus)
	}

	// A row below the panel is not consumed by it.
	if handled, _ := m.clickDebugPanel(2, top+m.debugPanelHeight(), false); handled {
		t.Fatal("a row below the panel must not be consumed by it")
	}

	// The wheel moves the selection of the column under the pointer.
	m.dapFocus, m.dapVarSel = 2, 0
	_ = m.handleMouseWheel(tea.MouseWheelMsg{X: threadW + frameW + 3, Y: top + 2, Button: tea.MouseWheelDown})
	if m.dapVarSel != 1 || m.dapFocus != 2 {
		t.Fatalf("wheel down: sel = %d focus = %d, want 1/2", m.dapVarSel, m.dapFocus)
	}

	// A long list scrolls so the selection stays on screen.
	m.dapVarStack = [][]dapVarRow{{}}
	for i := 0; i < 40; i++ {
		m.dapVarStack[0] = append(m.dapVarStack[0], dapVarRow{name: fmt.Sprintf("v%d", i), val: "0"})
	}
	m.dapVarSel = 39
	n := m.dapListRows()
	start := m.dapVarWindow(n)
	if start > 39 || 39 >= start+n {
		t.Fatalf("window start = %d with %d rows: selection 39 must stay visible", start, n)
	}
}

func TestDapRestartAfterEnded(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.dapOpen = true
	// A non-existent adapter makes the async start fail fast (LookPath), so
	// running the returned command cannot spawn a real debugger.
	m.cfg.Debug.AdapterCmd = "dmed-test-no-such-adapter"

	// While a session is running, F5 interrupts the debuggee (pause) instead
	// of doing nothing: a program that never reaches a breakpoint used to
	// leave F5 permanently dead.
	m.dapRunState = dapRunning
	m.dapClient = newStubDAPClient()
	pause := m.startDebugging()
	if pause == nil {
		m.dapClient.Close()
		t.Fatal("startDebugging must interrupt a running debuggee")
	}
	// The command is not run: it would block on a stub client that never
	// answers DAP requests.
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

// TestDapPanelPagingAndEdges covers the panel's navigation beyond one-step
// moves: a page is a screenful that stops at the ends (it used to be a fixed
// six entries that wrapped around, which loses the place in a long stack), and
// Home/End jump straight to the ends.
func TestDapPanelPagingAndEdges(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.dapOpen = true
	m.dapFocus = 2
	m.dapVarStack = [][]dapVarRow{{}}
	for i := 0; i < 40; i++ {
		m.dapVarStack[0] = append(m.dapVarStack[0], dapVarRow{name: fmt.Sprintf("v%d", i), val: "0"})
	}
	rows := m.dapListRows()
	if rows < 2 {
		t.Fatalf("test needs a multi-row panel, got %d rows", rows)
	}

	m = press(m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.dapVarSel != rows {
		t.Fatalf("pgdown = %d, want one page (%d)", m.dapVarSel, rows)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.dapVarSel != 2*rows {
		t.Fatalf("second pgdown = %d, want %d", m.dapVarSel, 2*rows)
	}
	for i := 0; i < 20; i++ {
		m = press(m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	if m.dapVarSel != 39 {
		t.Fatalf("pgdown past the end = %d, want the last entry 39", m.dapVarSel)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.dapVarSel != 39-rows {
		t.Fatalf("pgup = %d, want %d", m.dapVarSel, 39-rows)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyHome})
	if m.dapVarSel != 0 {
		t.Fatalf("home = %d, want the first entry", m.dapVarSel)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnd})
	if m.dapVarSel != 39 {
		t.Fatalf("end = %d, want the last entry", m.dapVarSel)
	}
	n := m.dapListRows()
	if start := m.dapVarWindow(n); start > 39 || 39 >= start+n {
		t.Fatalf("window start = %d with %d rows: the selection must stay visible", start, n)
	}
}

// TestDapPanelConsoleScrollKeys pins the keyboard scrolling of the console
// peek: with the console column focused the arrows, pages and ends walk the
// backlog (the offset counts lines back from the newest) instead of moving the
// hidden thread selection.
func TestDapPanelConsoleScrollKeys(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.dapOpen = true
	m.dapFocus = 0
	m.dapThreads = []dap.Thread{{ID: 1, Name: "main"}, {ID: 2, Name: "other"}}
	for i := 0; i < 100; i++ {
		m.dapAppendConsole(fmt.Sprintf("line %d", i))
	}
	m = press(m, tea.KeyPressMsg{Text: "l"})
	if !m.dapConsolePeek {
		t.Fatal("l must show the console")
	}

	m = press(m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.dapConsoleScroll != 1 {
		t.Fatalf("↑ = offset %d, want 1 (older)", m.dapConsoleScroll)
	}
	if m.dapSelThread != 0 {
		t.Fatalf("↑ must not move the thread selection, got %d", m.dapSelThread)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.dapConsoleScroll != 0 {
		t.Fatalf("↓ = offset %d, want back to the newest", m.dapConsoleScroll)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if want := m.dapListRows(); m.dapConsoleScroll != want {
		t.Fatalf("pgup = offset %d, want one page (%d)", m.dapConsoleScroll, want)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyHome})
	if want := len(m.dapConsole) - m.dapListRows(); m.dapConsoleScroll != want {
		t.Fatalf("home = offset %d, want the oldest (%d)", m.dapConsoleScroll, want)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnd})
	if m.dapConsoleScroll != 0 {
		t.Fatalf("end = offset %d, want the newest", m.dapConsoleScroll)
	}

	// The wheel over the console column scrolls it in the same direction as ↑.
	_ = m.handleMouseWheel(tea.MouseWheelMsg{X: 1, Y: m.dapPanelStartRow() + 2, Button: tea.MouseWheelUp})
	if m.dapConsoleScroll != 1 {
		t.Fatalf("wheel-up = offset %d, want 1 (older)", m.dapConsoleScroll)
	}
	_ = m.handleMouseWheel(tea.MouseWheelMsg{X: 1, Y: m.dapPanelStartRow() + 2, Button: tea.MouseWheelDown})
	if m.dapConsoleScroll != 0 {
		t.Fatalf("wheel-down = offset %d, want back to the newest", m.dapConsoleScroll)
	}

	m.dapClearConsole()
	if len(m.dapConsole) != 0 || m.dapConsoleScroll != 0 {
		t.Fatalf("clearing must empty the backlog, got %d lines, offset %d", len(m.dapConsole), m.dapConsoleScroll)
	}
}

// TestDapConsoleScrollAnchor keeps a reader who scrolled back through the
// console on the same lines while new output arrives: the offset counts from
// the newest line, so an append has to compensate for it.
func TestDapConsoleScrollAnchor(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.dapOpen = true
	for i := 0; i < 50; i++ {
		m.dapAppendConsole(fmt.Sprintf("line %d", i))
	}
	n := m.dapListRows()
	m.dapConsoleScroll = 5
	before, _, _ := m.dapConsoleWindow(n)
	m.dapAppendConsole("newest output")
	after, _, _ := m.dapConsoleWindow(n)
	if len(before) == 0 || len(after) == 0 || before[0] != after[0] {
		t.Fatalf("the console window moved while scrolled back: %q -> %q", before, after)
	}
}

// TestDapPanelResizeKeys covers +/-: the panel grows and shrinks a row at a
// time within what the terminal can spare, so a long stack or variable list can
// be given more room than the automatic quarter of the screen.
func TestDapPanelResizeKeys(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.dapOpen = true

	base := m.debugPanelHeight()
	m = press(m, tea.KeyPressMsg{Text: "+"})
	if got := m.debugPanelHeight(); got != base+1 {
		t.Fatalf("+ = %d rows, want %d", got, base+1)
	}
	// "=" is the same key on layouts where "+" needs Shift.
	m = press(m, tea.KeyPressMsg{Text: "="})
	if got := m.debugPanelHeight(); got != base+2 {
		t.Fatalf("= = %d rows, want %d", got, base+2)
	}
	for i := 0; i < 100; i++ {
		m = press(m, tea.KeyPressMsg{Text: "+"})
	}
	if got, max := m.debugPanelHeight(), m.height-dapPanelMinRows; got != max {
		t.Fatalf("growth must stop at %d rows, got %d", max, got)
	}
	// A taller panel lists more entries of the same data.
	tall := m.dapListRows()
	for i := 0; i < 100; i++ {
		m = press(m, tea.KeyPressMsg{Text: "-"})
	}
	if got := m.debugPanelHeight(); got != dapPanelMinRows {
		t.Fatalf("shrink must stop at %d rows, got %d", dapPanelMinRows, got)
	}
	if short := m.dapListRows(); short >= tall {
		t.Fatalf("a shorter panel must list fewer rows: %d -> %d", tall, short)
	}
}

// TestDapPanelWideSelection pins the answer to "the data does not fit": the row
// carrying the focused selection is drawn across the whole panel, so a value
// longer than its column stays readable.
func TestDapPanelWideSelection(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.dapOpen = true
	m.dapFocus = 2
	long := strings.Repeat("x", 60)
	m.dapVarStack = [][]dapVarRow{{{isScope: true, name: "Locals"}, {name: "s", val: long}}}
	m.dapVarSel = 1 // the value, not the scope header

	if v := m.View().Content; !strings.Contains(v, "s = "+long) {
		t.Fatalf("the selected value must be shown in full:\n%s", v)
	}
	// Unfocused, the same value stays clipped to its column: the full text is
	// the wide row's doing.
	m.dapFocus = 0
	m.dapThreads = []dap.Thread{{ID: 1, Name: "main"}}
	if v := m.View().Content; strings.Contains(v, long) {
		t.Fatalf("an unfocused long value must stay clipped to its column:\n%s", v)
	}
}
