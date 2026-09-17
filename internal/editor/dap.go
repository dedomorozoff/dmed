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

// dapLaunchMsg reports the outcome of the async launch sequence.
type dapLaunchMsg struct {
	err error
	gen int
}

// dapStartMsg reports the outcome of the async adapter start (spawn +
// initialize handshake). gen lets the UI drop results from a session that was
// already superseded by stop/restart.
type dapStartMsg struct {
	cl       *dap.Client
	gen      int
	supports bool
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
	adapterArgs := strings.Fields(m.cfg.Debug.AdapterArgs)
	root := m.baseDir()
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
		return dapStartMsg{cl: cl, gen: gen, supports: supports}
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
		for path, lines := range bps {
			if _, err := cl.SetBreakpoints(path, lines); err != nil {
				return dapLaunchMsg{err: fmt.Errorf("breakpoints: %w", err), gen: gen}
			}
		}
		if err := cl.Launch(launchArgs); err != nil {
			return dapLaunchMsg{err: err, gen: gen}
		}
		if supports {
			_ = cl.ConfigureDone()
		}
		return dapLaunchMsg{gen: gen}
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
// adapters like Delve), else ".".
func (m Model) dapProgram() string {
	if p := m.cfg.Debug.Program; p != "" {
		return p
	}
	if t := m.cur(); t != nil && t.path != "" {
		if st, err := os.Stat(t.path); err == nil {
			if st.IsDir() {
				return t.path
			}
			return filepath.Dir(t.path)
		}
	}
	return "."
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

// toggleDebugBreakpoint adds/removes a breakpoint at the cursor line and
// synchronizes it with the adapter (while a session is live).
func (m *Model) toggleDebugBreakpoint() tea.Cmd {
	t := m.cur()
	if t == nil || t.path == "" {
		return nil
	}
	abs, _ := filepath.Abs(t.path)
	ln := t.buf.CurLine() + 1
	if m.dapBreak[abs] == nil {
		m.dapBreak[abs] = map[int]bool{}
	}
	if m.dapBreak[abs][ln] {
		delete(m.dapBreak[abs], ln)
		if len(m.dapBreak[abs]) == 0 {
			delete(m.dapBreak, abs)
		}
	} else {
		m.dapBreak[abs][ln] = true
	}
	return m.dapSyncBreakpoints(abs)
}

// startDebugging launches (or, when a session is already live and paused,
// continues) the current program. F5. An ended session is torn down and
// restarted from scratch.
func (m *Model) startDebugging() tea.Cmd {
	if m.dapRunState == dapStopped {
		return m.dapStepCmd("continue")
	}
	if m.dapBusy || (m.dapClient != nil && m.dapRunState != dapEnded) {
		return nil // already launching/running
	}
	m.dapGen++
	m.dapRunState = dapLaunch
	m.dapBusy = true
	var cmds []tea.Cmd
	if m.dapClient != nil {
		cl := m.dapClient
		m.dapClient = nil
		m.dapThreads = nil
		m.dapFrames = nil
		m.dapVarStack = nil
		m.dapBPVerif = map[string]map[int]bool{}
		cmds = append(cmds, func() tea.Msg { cl.Close(); return nil })
	}
	cmds = append(cmds, m.dapStartCmd())
	return tea.Batch(cmds...)
}

// stopDebugging disconnects the session and tears the adapter down. Shift+F5.
func (m *Model) stopDebugging() tea.Cmd {
	cl := m.dapClient
	m.dapGen++
	m.dapRunState = dapIdle
	m.dapClient = nil
	m.dapThreads = nil
	m.dapFrames = nil
	m.dapVarStack = nil
	m.dapScopes = nil
	m.dapCurPath = ""
	m.dapCurLine = 0
	m.dapReason = ""
	m.dapBusy = false
	m.dapBPVerif = map[string]map[int]bool{}
	if cl == nil {
		return nil
	}
	return func() tea.Msg {
		cl.Close()
		return nil
	}
}

// handleDap routes keys while the debug panel is open. F4/F5/F10/F11/Shift+F5
// are handled globally in handleKey, so only panel-local keys land here.
// dapFocus 0-2 select the threads/stack/variables lists; focus 3 is the
// evaluator input row where every printable key is typed literally.
func (m *Model) handleDap(msg tea.KeyPressMsg) tea.Cmd {
	s := msg.String()

	if m.dapFocus == 3 {
		switch s {
		case "esc":
			if len(m.dapIn) > 0 {
				m.dapIn = nil
			} else {
				m.dapFocus = 0
			}
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
		if len(m.dapIn) > 0 {
			m.dapIn = nil
			return nil
		}
		if len(m.dapVarStack) > 1 {
			m.dapVarStack = m.dapVarStack[:len(m.dapVarStack)-1]
			return nil
		}
		m.dapOpen = false
		return nil
	case "backspace":
		if len(m.dapIn) > 0 {
			m.dapIn = m.dapIn[:len(m.dapIn)-1]
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
		m.dapMoveSel(-1)
	case "down", "j":
		m.dapMoveSel(1)
	case "pgup":
		m.dapMoveSel(-6)
	case "pgdown":
		m.dapMoveSel(6)
	case "ctrl+l":
		m.dapConsole = nil
		return nil
	default:
		if len(msg.Text) > 0 {
			// First typed character jumps to the input row so the rest of the
			// expression is typed literally (Tab focuses it explicitly when an
			// expression starts with a reserved navigation key).
			m.dapIn = append(m.dapIn, []rune(msg.Text)...)
			m.dapFocus = 3
		}
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

// ── DAP event handling ─────────────────────────────────────────────────────

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
	case dap.EventTerminated:
		m.dapRunState = dapEnded
		m.dapCurPath = ""
		m.dapCurLine = 0
		m.dapConsole = append(m.dapConsole, "debug session terminated")
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
