package editor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/dap"
)

// dapEventMsg carries a decoded DAP server event from the adapter's read loop
// into the model (Mirror of lspDiagMsg). gen stamps the session that produced
// it so stale events from a superseded session are dropped.
type dapEventMsg struct {
	ev  dap.Event
	gen int
}

// dapLaunchMsg reports the outcome of the async launch sequence. bps carries
// the adapter's verification of every breakpoint pushed during the launch so
// the gutter can tell a real breakpoint (●) from a rejected one (○).
type dapLaunchMsg struct {
	err error
	gen int
	bps []dapBPSyncMsg
}

// dapStartMsg reports the outcome of the async adapter start (spawn +
// initialize handshake). gen lets the UI drop results from a session that was
// already superseded by stop/restart.
type dapStartMsg struct {
	cl       *dap.Client
	gen      int
	supports bool
	adapter  string // resolved adapter base name, for the status bar
	err      error
}

// dapStepMsg reports the outcome of continue/next/stepIn/stepOut.
type dapStepMsg struct {
	err error
}

// dapRefreshMsg carries a complete snapshot of threads/frames/scopes/variables
// fetched after a stop or a selection change.
type dapRefreshMsg struct {
	err       error
	threads   []dap.Thread
	frames    []dap.StackFrame
	scopes    []dap.Scope
	vars      []dapVarRow
	scopePush bool // vars replace (not merge into) the current tree level
}

// dapVarRow is one rendered variables-panel line: a scope header or a variable.
type dapVarRow struct {
	isScope bool
	name    string
	val     string
	ref     int64
	typ     string
}

// dapEvalMsg delivers the result of a console expression.
type dapEvalMsg struct {
	err  error
	expr string
	out  string
}

// dapBPSyncMsg reports the adapter's verification of a setBreakpoints request.
type dapBPSyncMsg struct {
	err      error
	path     string
	lines    []int
	verified []bool
}

// waitForDAPEvent re-arms the DAP event channel drain (mirror of waitForLSPDiag).
func waitForDAPEvent(ch chan dapEventMsg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

// dapRunState values shown in the panel header.
const (
	dapIdle    = ""       // no live session
	dapLaunch  = "launch" // building/starting the debuggee
	dapRunning = "run"    // executing
	dapStopped = "stop"   // paused at a breakpoint/step/pause
	dapEnded   = "end"    // process exited, session still alive
)

// dapAdapterCommand resolves the effective adapter binary and argv for a DAP
// session. Delve exposes the DAP server under its `dap` subcommand, so the
// default "dlv" is always invoked as `dlv dap ...`; generic adapters
// (debugpy, lldb-dap, ...) take the given args verbatim.
func dapAdapterCommand(adapter, args string) (string, []string) {
	argv := []string{}
	if args != "" {
		argv = strings.Fields(args)
	}
	base := strings.ToLower(filepath.Base(adapter))
	if base == "dlv" || base == "dlv.exe" {
		if len(argv) == 0 || argv[0] != "dap" {
			argv = append([]string{"dap"}, argv...)
		}
	}
	return adapter, argv
}

// dapStartCmd spawns the DAP adapter and completes the initialize handshake
// in the background so the UI never blocks on adapter startup. The outcome
// arrives as a dapStartMsg stamped with the session generation; results from
// superseded sessions are dropped by the Update handler.
func (m *Model) dapStartCmd() tea.Cmd {
	adapter := m.cfg.Debug.AdapterCmd
	if adapter == "" {
		adapter = "dlv"
	}
	mode := m.cfg.Debug.AdapterMode
	if mode == "" {
		mode = "reverse"
	}
	adapter, adapterArgs := dapAdapterCommand(adapter, m.cfg.Debug.AdapterArgs)
	root := m.baseDir()
	if p := m.dapProgram(); p != "" {
		// dlv runs `go build <program>` in its own working directory, so the
		// adapter must be launched from the program's directory: an absolute
		// program path outside dlv's cwd module fails with "directory ...
		// outside main module". The debuggee's own cwd is set separately via
		// the launch `cwd` argument.
		if st, err := os.Stat(p); err == nil {
			if st.IsDir() {
				root = p
			} else {
				root = filepath.Dir(p)
			}
			if abs, err := filepath.Abs(root); err == nil {
				root = abs
			}
		}
	}
	ch := m.dapCh
	gen := m.dapGen
	return func() tea.Msg {
		if _, err := exec.LookPath(adapter); err != nil {
			return dapStartMsg{gen: gen, err: fmt.Errorf("debug: %s not found — install it or set [debug] adapter_cmd", adapter)}
		}
		onEvent := func(e dap.Event) {
			select {
			case ch <- dapEventMsg{ev: e, gen: gen}:
			default: // drop if the UI is backed up
			}
		}
		var (
			cl  *dap.Client
			err error
		)
		if mode == "stdio" {
			cl, err = dap.StartStdio(adapter, adapterArgs, root, onEvent)
		} else {
			cl, err = dap.StartReverse(adapter, adapterArgs, root, onEvent)
		}
		if err != nil {
			return dapStartMsg{gen: gen, err: err}
		}
		supports, err := cl.Initialize()
		if err != nil {
			cl.Close()
			return dapStartMsg{gen: gen, err: fmt.Errorf("debug adapter initialize: %w", err)}
		}
		return dapStartMsg{cl: cl, gen: gen, supports: supports, adapter: filepath.Base(adapter)}
	}
}

// dapLaunchCmd starts the debuggee: it pushes the currently set breakpoints,
// sends launch, then configurationDone so the adapter begins execution.
func (m *Model) dapLaunchCmd() tea.Cmd {
	cl := m.dapClient
	if cl == nil {
		return nil
	}
	bps := m.dapBreakSnapshot()
	supports := m.dapSupportsConfigDone
	launchArgs, err := m.dapLaunchArgs()
	gen := m.dapGen
	return func() tea.Msg {
		if err != nil {
			return dapLaunchMsg{err: err, gen: gen}
		}
		// Order matters: the adapter only accepts source breakpoints once the
		// launch/attach request has created the debug session (Delve answers
		// "No debug session started" otherwise), yet they must land before
		// configurationDone lets the debuggee run — the order VS Code uses.
		// Pushing them first aborted the launch before it started, which also
		// left F5 permanently stuck on the "session already attached" guard.
		if err := cl.Launch(launchArgs); err != nil {
			return dapLaunchMsg{err: err, gen: gen}
		}
		paths := make([]string, 0, len(bps))
		for path := range bps {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		var sync []dapBPSyncMsg
		for _, path := range paths {
			res, err := cl.SetBreakpoints(path, bps[path])
			if err != nil {
				return dapLaunchMsg{err: fmt.Errorf("breakpoints: %w", err), gen: gen}
			}
			lines := make([]int, len(res))
			verified := make([]bool, len(res))
			for i, b := range res {
				lines[i] = b.Line
				verified[i] = b.Verified
			}
			sync = append(sync, dapBPSyncMsg{path: path, lines: lines, verified: verified})
		}
		if supports {
			_ = cl.ConfigureDone()
		}
		return dapLaunchMsg{gen: gen, bps: sync}
	}
}

// dapLaunchArgs composes the adapter-specific launch/attach body from the
// [debug] config. Adapter-specific keys from launch_json override the
// built-in ones, so any DAP adapter can be driven from config.
func (m Model) dapLaunchArgs() (map[string]interface{}, error) {
	args := map[string]interface{}{
		"request": m.cfg.Debug.LaunchRequest,
		"type":    m.cfg.Debug.LaunchType,
	}
	if m.cfg.Debug.Mode != "" {
		args["mode"] = m.cfg.Debug.Mode
	}
	if p := m.dapProgram(); p != "" {
		args["program"] = p
	}
	if c := m.dapCwd(); c != "" {
		args["cwd"] = c
	}
	if m.cfg.Debug.StopOnEntry {
		args["stopOnEntry"] = true
	}
	if a := strings.Fields(m.cfg.Debug.Args); len(a) > 0 {
		args["args"] = a
	}
	if m.cfg.Debug.LaunchJSON == "" {
		return args, nil
	}
	var extra map[string]interface{}
	if err := json.Unmarshal([]byte(m.cfg.Debug.LaunchJSON), &extra); err != nil {
		return nil, fmt.Errorf("debug launch_json: %w", err)
	}
	for k, v := range extra {
		args[k] = v
	}
	return args, nil
}

// dapProgram resolves what to debug: the [debug] program setting, else the
// directory of the active file (a sensible default for package-oriented
// adapters like Delve), else ".". For a Go file outside any module, the file
// itself is returned: dlv then builds `go build <file>.go`, which works
// without a go.mod, instead of failing on a module-less package directory.
func (m Model) dapProgram() string {
	if p := m.cfg.Debug.Program; p != "" {
		return p
	}
	if t := m.cur(); t != nil && t.path != "" {
		if filepath.Ext(t.path) == ".go" && !pathInGoModule(filepath.Dir(t.path)) {
			return t.path
		}
		if st, err := os.Stat(t.path); err == nil {
			if st.IsDir() {
				return t.path
			}
			return filepath.Dir(t.path)
		}
	}
	return "."
}

// pathInGoModule reports whether dir (or any ancestor) carries a go.mod or
// go.work file, i.e. belongs to a module the Go tool can build as a package.
func pathInGoModule(dir string) bool {
	for {
		for _, name := range []string{"go.mod", "go.work"} {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				return true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// dapCwd is the working directory for the launched program.
func (m Model) dapCwd() string {
	if m.root != "" {
		return m.root
	}
	if t := m.cur(); t != nil && t.path != "" {
		return filepath.Dir(t.path)
	}
	return "."
}

// dapBreakSnapshot folds the breakpoint map into per-path sorted line lists.
func (m Model) dapBreakSnapshot() map[string][]int {
	out := make(map[string][]int, len(m.dapBreak))
	for path, set := range m.dapBreak {
		ls := make([]int, 0, len(set))
		for l := range set {
			ls = append(ls, l)
		}
		sort.Ints(ls)
		out[path] = ls
	}
	return out
}

// dapSelThreadID returns the ID of the user-selected (or first) thread.
func (m Model) dapSelThreadID() int64 {
	if m.dapSelThread >= 0 && m.dapSelThread < len(m.dapThreads) {
		return m.dapThreads[m.dapSelThread].ID
	}
	if len(m.dapThreads) > 0 {
		return m.dapThreads[0].ID
	}
	return 1
}

// dapSelFrameID returns the ID of the user-selected (or top) frame, or -1.
func (m Model) dapSelFrameID() int64 {
	if m.dapSelFrame >= 0 && m.dapSelFrame < len(m.dapFrames) {
		return m.dapFrames[m.dapSelFrame].ID
	}
	return -1
}

// dapContinueCmd resumes execution of the selected thread.
func (m *Model) dapStepCmd(op string) tea.Cmd {
	cl := m.dapClient
	if cl == nil {
		return nil
	}
	threadID := m.dapSelThreadID()
	var (
		err error
	)
	return func() tea.Msg {
		switch op {
		case "continue":
			err = cl.Continue(threadID)
		case "next":
			err = cl.Next(threadID)
		case "stepIn":
			err = cl.StepIn(threadID)
		case "stepOut":
			err = cl.StepOut(threadID)
		case "pause":
			err = cl.Pause(threadID)
		}
		if err != nil {
			return dapStepMsg{err: err}
		}
		return dapStepMsg{}
	}
}

// dapRefreshCmd asynchronously pulls a full threads/frames/scopes/variables
// snapshot so the panel can repaint after a stop or selection change.
func (m *Model) dapRefreshCmd() tea.Cmd {
	cl := m.dapClient
	if cl == nil {
		return nil
	}
	threadID := m.dapSelThreadID()
	frameID := m.dapSelFrameID()
	return func() tea.Msg {
		threads, err := cl.Threads()
		if err != nil {
			return dapRefreshMsg{err: err}
		}
		frames, err := cl.StackTrace(threadID, 40)
		if err != nil {
			return dapRefreshMsg{err: err, threads: threads}
		}
		selFrame := frameID
		if selFrame < 0 && len(frames) > 0 {
			selFrame = frames[0].ID
		}
		scopes, err := cl.Scopes(selFrame)
		if err != nil {
			return dapRefreshMsg{err: err, threads: threads, frames: frames}
		}
		var vars []dapVarRow
		for _, s := range scopes {
			if s.VariablesRef <= 0 || s.Expensive {
				continue
			}
			vs, err := cl.Variables(s.VariablesRef)
			if err != nil {
				continue
			}
			vars = append(vars, dapVarRow{isScope: true, name: s.Name})
			for _, v := range vs {
				vars = append(vars, dapVarRow{name: v.Name, val: v.Value, ref: v.VariablesRef, typ: v.Type})
			}
		}
		return dapRefreshMsg{threads: threads, frames: frames, scopes: scopes, vars: vars}
	}
}

// dapExpandVarsCmd fetches the children of a variable for the variables tree.
func (m *Model) dapExpandVarsCmd(ref int64) tea.Cmd {
	cl := m.dapClient
	if cl == nil || ref <= 0 {
		return nil
	}
	return func() tea.Msg {
		vs, err := cl.Variables(ref)
		if err != nil {
			return dapRefreshMsg{err: err}
		}
		var rows []dapVarRow
		for _, v := range vs {
			rows = append(rows, dapVarRow{name: v.Name, val: v.Value, ref: v.VariablesRef, typ: v.Type})
		}
		return dapRefreshMsg{vars: rows, scopePush: true}
	}
}

// dapEvalCmd evaluates an expression in the context of the selected frame.
func (m *Model) dapEvalCmd(expr string) tea.Cmd {
	cl := m.dapClient
	if cl == nil || expr == "" {
		return nil
	}
	frameID := int64(-1)
	if m.dapRunState == dapStopped {
		frameID = m.dapSelFrameID()
	}
	return func() tea.Msg {
		v, err := cl.Evaluate(expr, frameID)
		if err != nil {
			return dapEvalMsg{err: err, expr: expr}
		}
		return dapEvalMsg{expr: expr, out: v.Value + " (" + v.Type + ")"}
	}
}

// dapSyncBreakpoints pushes the current breakpoints of one file to the
// adapter and reports verification asynchronously.
func (m *Model) dapSyncBreakpoints(path string) tea.Cmd {
	cl := m.dapClient
	if cl == nil {
		return nil
	}
	lines := m.dapBreak[path]
	if len(lines) == 0 {
		lines = nil
	}
	ls := make([]int, 0, len(lines))
	for l := range lines {
		ls = append(ls, l)
	}
	sort.Ints(ls)
	return func() tea.Msg {
		bps, err := cl.SetBreakpoints(path, ls)
		if err != nil {
			return dapBPSyncMsg{err: err, path: path}
		}
		verified := make([]bool, len(bps))
		lines := make([]int, len(bps))
		for i, b := range bps {
			verified[i] = b.Verified
			lines[i] = b.Line
		}
		return dapBPSyncMsg{path: path, lines: lines, verified: verified}
	}
}

// toggleBreakpointAt adds/removes a breakpoint at the given 0-based line of
// the active tab and synchronizes it with the adapter (while a session is
// live).
func (m *Model) toggleBreakpointAt(ln int) tea.Cmd {
	t := m.cur()
	if t == nil || t.path == "" {
		return nil
	}
	abs, _ := filepath.Abs(t.path)
	line := ln + 1
	if m.dapBreak[abs] == nil {
		m.dapBreak[abs] = map[int]bool{}
	}
	if m.dapBreak[abs][line] {
		delete(m.dapBreak[abs], line)
		if len(m.dapBreak[abs]) == 0 {
			delete(m.dapBreak, abs)
		}
	} else {
		m.dapBreak[abs][line] = true
	}
	return m.dapSyncBreakpoints(abs)
}

// toggleDebugBreakpoint adds/removes a breakpoint at the cursor line and
// synchronizes it with the adapter (while a session is live). F4.
func (m *Model) toggleDebugBreakpoint() tea.Cmd {
	t := m.cur()
	if t == nil || t.path == "" {
		return nil
	}
	return m.toggleBreakpointAt(t.buf.CurLine())
}

// startDebugging launches (or, when a session is already live and paused,
// continues) the current program. F5. While the debuggee is running F5
// interrupts it, and an ended session is torn down and restarted from scratch.
func (m *Model) startDebugging() tea.Cmd {
	switch m.dapRunState {
	case dapStopped:
		return m.dapStepCmd("continue")
	case dapRunning:
		// Without this a program that never reaches a breakpoint (or simply
		// runs long) left F5 a dead key: the "already attached" guard below
		// refused to do anything, and there was no way to interrupt the run.
		return m.dapStepCmd("pause")
	}
	if m.dapBusy || (m.dapClient != nil && m.dapRunState != dapEnded) {
		return nil // already launching/running
	}
	m.dapGen++
	m.dapRunState = dapLaunch
	m.dapBusy = true
	var cmds []tea.Cmd
	if m.dapClient != nil {
		// An ended session must be released before the new adapter starts.
		cmds = append(cmds, m.dapRelease())
	}
	cmds = append(cmds, m.dapStartCmd())
	return tea.Batch(cmds...)
}

// stopDebugging disconnects the session and tears the adapter down. Shift+F5.
func (m *Model) stopDebugging() tea.Cmd {
	m.dapGen++
	m.dapRunState = dapIdle
	m.dapBusy = false
	return m.dapRelease()
}

// dapRelease detaches the client and clears every piece of derived session
// state, returning a command that closes the adapter (nil when none was
// attached). The generation is left alone so callers decide whether the
// session counts as superseded.
func (m *Model) dapRelease() tea.Cmd {
	cl := m.dapClient
	m.dapClient = nil
	m.dapThreads = nil
	m.dapFrames = nil
	m.dapVarStack = nil
	m.dapScopes = nil
	m.dapCurPath = ""
	m.dapCurLine = 0
	m.dapReason = ""
	m.dapBPVerif = map[string]map[int]bool{}
	if cl == nil {
		return nil
	}
	return func() tea.Msg {
		cl.Close()
		return nil
	}
}

// handleDap routes keys while the debug panel is open. F4/F5/F6/F7/Shift+F5
// are handled globally in handleKey, so only panel-local keys land here.
// dapFocus 0-2 select the threads/stack/variables lists; focus 3 is the
// evaluator input row, reached only explicitly via Tab (typed characters stay
// out of it so the panel never hijacks ordinary editing keys).
func (m *Model) handleDap(msg tea.KeyPressMsg) tea.Cmd {
	s := msg.String()

	if m.dapFocus == 3 {
		switch s {
		case "esc":
			m.dapIn = nil
			m.dapFocus = 0
			m.dapOpen = false
		case "backspace":
			if len(m.dapIn) > 0 {
				m.dapIn = m.dapIn[:len(m.dapIn)-1]
			}
		case "enter":
			if len(m.dapIn) > 0 {
				expr := string(m.dapIn)
				m.dapIn = nil
				return m.dapEvalCmd(expr)
			}
		case "tab":
			m.dapFocus = (m.dapFocus + 1) % 4
		case "shift+tab":
			m.dapFocus = (m.dapFocus + 3) % 4
		case "ctrl+l":
			m.dapConsole = nil
		default:
			if len(msg.Text) > 0 {
				m.dapIn = append(m.dapIn, []rune(msg.Text)...)
			}
		}
		return nil
	}

	switch s {
	case "esc":
		m.dapIn = nil
		m.dapFocus = 0
		m.dapOpen = false
		return nil
	case "backspace", "left":
		if len(m.dapIn) > 0 {
			m.dapIn = m.dapIn[:len(m.dapIn)-1]
			return nil
		}
		// Walking back out of an expanded variable: expanding replaces the
		// visible level, so this is the only way back to the parent scope.
		if m.dapFocus == 2 && len(m.dapVarStack) > 1 {
			m.dapVarStack = m.dapVarStack[:len(m.dapVarStack)-1]
			m.dapVarSel = 0
		}
		return nil
	case "l":
		m.dapConsolePeek = !m.dapConsolePeek
		return nil
	case "enter":
		if m.dapFocus == 2 {
			if v := m.dapSelectedVar(); v != nil && v.ref > 0 {
				return m.dapExpandVarsCmd(v.ref)
			}
		}
		return nil
	case "tab":
		m.dapFocus = (m.dapFocus + 1) % 4 // 0..3, incl. the input row
		return nil
	case "shift+tab":
		m.dapFocus = (m.dapFocus + 3) % 4
		return nil
	case "up", "k":
		return m.dapMoveSelCmd(-1)
	case "down", "j":
		return m.dapMoveSelCmd(1)
	case "pgup":
		return m.dapMoveSelCmd(-6)
	case "pgdown":
		return m.dapMoveSelCmd(6)
	case "ctrl+l":
		m.dapConsole = nil
		return nil
	default:
		// Typed characters are deliberately ignored here: the evaluator row is
		// opt-in via Tab (or a click on it once it is focused), so ordinary
		// editing keys are never hijacked by the debug panel.
	}
	return nil
}

// dapSelectedVar is the variable currently highlighted in the variables list.
func (m Model) dapSelectedVar() *dapVarRow {
	rows := m.dapCurrentVars()
	if m.dapVarSel < 0 || m.dapVarSel >= len(rows) {
		return nil
	}
	r := rows[m.dapVarSel]
	if r.isScope {
		return nil
	}
	return &r
}

// dapCurrentVars is the top of the variable navigation stack.
func (m Model) dapCurrentVars() []dapVarRow {
	if len(m.dapVarStack) == 0 {
		return nil
	}
	return m.dapVarStack[len(m.dapVarStack)-1]
}

// dapMoveSel moves the panel selection within the focused list.
func (m *Model) dapMoveSel(d int) {
	n := 0
	switch m.dapFocus {
	case 0:
		n = len(m.dapThreads)
		m.dapSelThread = (m.dapSelThread + d + n) % maxInt(n, 1)
	case 1:
		n = len(m.dapFrames)
		m.dapSelFrame = (m.dapSelFrame + d + n) % maxInt(n, 1)
	default:
		n = len(m.dapCurrentVars())
		m.dapVarSel = (m.dapVarSel + d + n) % maxInt(n, 1)
	}
}

// dapMoveSelCmd moves the panel selection like dapMoveSel and reloads the
// snapshot when the move changes what the other columns show: another thread
// carries its own stack, another frame its own variables. Selecting a thread
// or frame used to leave the stack/variables pane on stale data.
func (m *Model) dapMoveSelCmd(d int) tea.Cmd {
	switch m.dapFocus {
	case 0:
		if len(m.dapThreads) == 0 {
			return nil
		}
		before := m.dapSelThread
		m.dapMoveSel(d)
		if m.dapSelThread == before {
			return nil
		}
		return m.dapRefreshCmd()
	case 1:
		if len(m.dapFrames) == 0 {
			return nil
		}
		before := m.dapSelFrame
		m.dapMoveSel(d)
		if m.dapSelFrame == before {
			return nil
		}
		return m.dapRefreshCmd()
	}
	m.dapMoveSel(d)
	return nil
}

// dapSelectThread/frame/var are the mouse entry points into the panel: they
// move the focus and selection exactly like the arrow keys, reloading the
// derived columns when the pick changes them.
func (m *Model) dapSelectThread(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.dapThreads) {
		return nil
	}
	m.dapFocus = 0
	if m.dapSelThread == idx {
		return nil
	}
	m.dapSelThread = idx
	m.dapSelFrame = 0
	return m.dapRefreshCmd()
}

func (m *Model) dapSelectFrame(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.dapFrames) {
		return nil
	}
	m.dapFocus = 1
	if m.dapSelFrame == idx {
		return nil
	}
	m.dapSelFrame = idx
	return m.dapRefreshCmd()
}

// dapSelectVar highlights a variable row; expand asks for its children the
// way pressing Enter does.
func (m *Model) dapSelectVar(idx int, expand bool) tea.Cmd {
	vars := m.dapCurrentVars()
	if idx < 0 || idx >= len(vars) {
		return nil
	}
	m.dapFocus = 2
	m.dapVarSel = idx
	if !expand {
		return nil
	}
	if v := vars[idx]; v.ref > 0 {
		return m.dapExpandVarsCmd(v.ref)
	}
	return nil
}


// handleDAPEventUpdate routes an adapter event into panel state and re-arms
// the event channel. Events from a superseded session are dropped so a stale
// disconnect/terminated can never clobber a newer live session.
func (m *Model) handleDAPEventUpdate(msg dapEventMsg) tea.Cmd {
	if msg.gen != 0 && msg.gen != m.dapGen {
		return waitForDAPEvent(m.dapCh)
	}
	cmd := m.handleDAPEvent(msg.ev)
	if cmd != nil {
		return tea.Batch(cmd, waitForDAPEvent(m.dapCh))
	}
	return waitForDAPEvent(m.dapCh)
}

// applyDAPRefresh merges a fetched threads/frames/variables snapshot into the
// panel state. The stopped location is always re-derived from the top stack
// frame so the ▶ marker survives even when scopes/variables requests fail.
func (m *Model) applyDAPRefresh(msg dapRefreshMsg) {
	if msg.err != nil {
		m.msg = "debug refresh: " + msg.err.Error()
	}
	if msg.threads != nil {
		m.dapThreads = msg.threads
		if m.dapSelThread >= len(m.dapThreads) && len(m.dapThreads) > 0 {
			m.dapSelThread = 0
		}
	}
	if msg.frames != nil {
		m.dapFrames = msg.frames
		if m.dapSelFrame >= len(m.dapFrames) && len(m.dapFrames) > 0 {
			m.dapSelFrame = 0
		}
	}
	if msg.scopes != nil {
		m.dapScopes = msg.scopes
	}
	if msg.vars == nil {
		m.updateDapCurrentLocation()
		return
	}
	if msg.scopePush {
		m.dapVarStack = append(m.dapVarStack, msg.vars)
	} else {
		m.dapVarStack = [][]dapVarRow{msg.vars}
	}
	m.dapVarSel = 0
	m.updateDapCurrentLocation()
}

// applyDapBPSyncs merges adapter breakpoint verifications into the gutter map
// so a rejected line renders as ○ instead of ●.
func (m *Model) applyDapBPSyncs(syncs []dapBPSyncMsg) {
	for _, s := range syncs {
		if s.err != nil || s.path == "" {
			continue
		}
		if m.dapBPVerif[s.path] == nil {
			m.dapBPVerif[s.path] = map[int]bool{}
		}
		for i, l := range s.lines {
			if i < len(s.verified) {
				m.dapBPVerif[s.path][l] = s.verified[i]
			}
		}
	}
}

// updateDapCurrentLocation syncs the ▶ marker to the selected stack frame.
func (m *Model) updateDapCurrentLocation() {
	if len(m.dapFrames) > 0 && m.dapSelFrame < len(m.dapFrames) {
		f := m.dapFrames[m.dapSelFrame]
		if f.Path != "" {
			m.dapCurPath = f.Path
			m.dapCurLine = f.Line
		}
	}
}

func (m *Model) handleDAPEvent(ev dap.Event) tea.Cmd {
	switch ev.Kind {
	case dap.EventStopped:
		m.dapRunState = dapStopped
		m.dapReason = ev.Reason
		m.dapThreads = nil
		m.dapFrames = nil
		m.dapVarStack = nil
		m.dapSelThread = 0
		m.dapSelFrame = 0
		m.dapVarSel = 0
		m.dapCurPath = ev.SourcePath
		m.dapCurLine = ev.Line
		// Reveal the stopped location in the editor so the ▶ marker is visible.
		if m.dapCurPath != "" {
			m.focusOrOpen(m.dapCurPath)
			if t := m.cur(); t != nil && m.dapCurLine > 0 {
				t.buf.SetCursor(m.dapCurLine-1, 0)
				t.buf.Deselect()
				m.clampScroll()
			}
		}
		m.dapOpen = true
		m.termOpen = false
		return m.dapRefreshCmd()
	case dap.EventContinued:
		m.dapRunState = dapRunning
		m.dapCurPath = ""
		m.dapCurLine = 0
		// Frames/variables from the previous stop are stale while running.
		m.dapFrames = nil
		m.dapVarStack = nil
	case dap.EventOutput:
		line := ev.Output
		if line == "" {
			return nil
		}
		line = strings.TrimRight(line, "\r\n")
		if len(line) == 0 {
			return nil
		}
		switch ev.OutputCat {
		case "stderr":
			line = "err | " + line
		case "console":
			line = "dbg | " + line
		}
		m.dapConsole = append(m.dapConsole, line)
		if len(m.dapConsole) > 1000 {
			m.dapConsole = m.dapConsole[len(m.dapConsole)-1000:]
		}
	case dap.EventExited:
		m.dapConsole = append(m.dapConsole, fmt.Sprintf("process exited with code %d", ev.ExitCode))
		m.msg = fmt.Sprintf("debug: process exited with code %d", ev.ExitCode)
	case dap.EventTerminated:
		m.dapRunState = dapEnded
		m.dapCurPath = ""
		m.dapCurLine = 0
		m.dapConsole = append(m.dapConsole, "debug session terminated")
		m.msg = "debug: session ended"
	case dap.EventBreakpoint:
		if ev.SourcePath != "" {
			if m.dapBPVerif[ev.SourcePath] == nil {
				m.dapBPVerif[ev.SourcePath] = map[int]bool{}
			}
			m.dapBPVerif[ev.SourcePath][ev.Line] = ev.BPVerified
		}
	case dap.EventDisconnected:
		m.dapRunState = dapIdle
		m.dapClient = nil
		m.dapThreads = nil
		m.dapFrames = nil
		m.dapVarStack = nil
		m.dapScopes = nil
		m.dapCurPath = ""
		m.dapCurLine = 0
		m.dapReason = ""
		m.dapBPVerif = map[string]map[int]bool{}
	default:
		// EventThread, EventProcess, EventInitialized: no UI effect.
	}
	return nil
}
