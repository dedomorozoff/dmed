package editor

import (
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
// into the model (Mirror of lspDiagMsg).
type dapEventMsg struct {
	ev dap.Event
}

// dapLaunchMsg reports the outcome of the async launch sequence.
type dapLaunchMsg struct {
	err error
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

// ensureDAP lazily spawns the Delve DAP adapter in reverse-connect mode and
// completes the initialize handshake. It is fast (adapter connects on
// startup) and is called synchronously from the key handler so the launch
// itself can then run in the background.
func (m *Model) ensureDAP() error {
	if m.dapClient != nil {
		return nil
	}
	dlv := m.cfg.Debug.DlvPath
	if dlv == "" {
		dlv = "dlv"
	}
	if _, err := exec.LookPath(dlv); err != nil {
		return fmt.Errorf("debug: %s not found — go install github.com/go-delve/delve/cmd/dlv@latest", dlv)
	}
	root := m.baseDir()
	cl, err := dap.StartReverse(dlv, nil, root, func(e dap.Event) {
		select {
		case m.dapCh <- dapEventMsg{ev: e}:
		default: // drop if the UI is backed up
		}
	})
	if err != nil {
		return err
	}
	supports, err := cl.Initialize()
	if err != nil {
		cl.Close()
		return fmt.Errorf("debug adapter initialize: %w", err)
	}
	m.dapClient = cl
	m.dapSupportsConfigDone = supports
	return nil
}

// dapLaunchCmd starts the debuggee: it pushes the currently set breakpoints,
// sends launch, then configurationDone so the adapter begins execution.
func (m *Model) dapLaunchCmd() tea.Cmd {
	cl := m.dapClient
	if cl == nil {
		return nil
	}
	mode := m.cfg.Debug.Mode
	program := m.dapProgram()
	cwd := m.dapCwd()
	args := strings.Fields(m.cfg.Debug.Args)
	stopOnEntry := m.cfg.Debug.StopOnEntry
	supports := m.dapSupportsConfigDone
	bps := m.dapBreakSnapshot()
	return func() tea.Msg {
		for path, lines := range bps {
			_, _ = cl.SetBreakpoints(path, lines)
		}
		if err := cl.Launch(mode, "dmed session", program, cwd, args, stopOnEntry); err != nil {
			return dapLaunchMsg{err: err}
		}
		if supports {
			_ = cl.ConfigureDone()
		}
		return dapLaunchMsg{}
	}
}

// dapProgram resolves what to debug: the [debug] program setting, else the
// directory of the active file (Delve builds packages by directory), else ".".
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
// continues) the current program. F5.
func (m *Model) startDebugging() tea.Cmd {
	if m.dapRunState == dapStopped {
		return m.dapStepCmd("continue")
	}
	if m.dapClient != nil {
		return nil // already running/launching
	}
	if err := m.ensureDAP(); err != nil {
		m.msg = err.Error()
		return nil
	}
	m.dapRunState = dapLaunch
	m.dapBusy = true
	return tea.Batch(m.dapLaunchCmd())
}

// stopDebugging disconnects the session and tears the adapter down. Shift+F5.
func (m *Model) stopDebugging() tea.Cmd {
	cl := m.dapClient
	if cl == nil {
		return nil
	}
	m.dapRunState = dapIdle
	m.dapClient = nil
	m.dapThreads = nil
	m.dapFrames = nil
	return func() tea.Msg {
		cl.Close()
		return nil
	}
}

// handleDap routes keys while the debug panel is open.
func (m *Model) handleDap(msg tea.KeyPressMsg) tea.Cmd {
	s := msg.String()
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
	case "f4":
		return m.toggleDebugBreakpoint()
	case "f5":
		return m.startDebugging()
	case "shift+f5":
		return m.stopDebugging()
	case "f10":
		if m.dapRunState == dapStopped {
			return m.dapStepCmd("next")
		}
	case "f11":
		if m.dapRunState == dapStopped {
			return m.dapStepCmd("stepIn")
		}
	case "shift+f11":
		if m.dapRunState == dapStopped {
			return m.dapStepCmd("stepOut")
		}
	case "l":
		m.dapConsolePeek = !m.dapConsolePeek
		return nil
	case "enter":
		var cmds []tea.Cmd
		if m.dapFocus == 2 {
			if v := m.dapSelectedVar(); v != nil && v.ref > 0 {
				cmds = append(cmds, m.dapExpandVarsCmd(v.ref))
			}
		}
		if len(m.dapIn) > 0 {
			expr := string(m.dapIn)
			m.dapIn = nil
			cmds = append(cmds, m.dapEvalCmd(expr))
		}
		if len(cmds) == 0 {
			return nil
		}
		return tea.Batch(cmds...)
	case "tab":
		m.dapFocus = (m.dapFocus + 1) % 3 // threads | frames | variables
	case "shift+tab":
		m.dapFocus = (m.dapFocus + 2) % 3
	case "up", "k":
		m.dapMoveSel(-1)
	case "down", "j":
		m.dapMoveSel(1)
	case "pgup":
		m.dapMoveSel(-6)
	case "pgdn":
		m.dapMoveSel(6)
	case "ctrl+l":
		m.dapConsole = nil
		return nil
	default:
		if len(msg.Text) > 0 {
			m.dapIn = append(m.dapIn, []rune(msg.Text)...)
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
// the event channel.
func (m *Model) handleDAPEventUpdate(msg dapEventMsg) tea.Cmd {
	cmd := m.handleDAPEvent(msg.ev)
	if cmd != nil {
		return tea.Batch(cmd, waitForDAPEvent(m.dapCh))
	}
	return waitForDAPEvent(m.dapCh)
}

// applyDAPRefresh merges a fetched threads/frames/variables snapshot into the
// panel state.
func (m *Model) applyDAPRefresh(msg dapRefreshMsg) {
	if msg.err != nil {
		m.msg = "debug refresh: " + msg.err.Error()
		return
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
		return
	}
	if msg.scopePush {
		m.dapVarStack = append(m.dapVarStack, msg.vars)
	} else {
		m.dapVarStack = [][]dapVarRow{msg.vars}
	}
	m.dapVarSel = 0
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
		m.dapCurPath = ""
		m.dapCurLine = 0
		m.dapReason = ""
	default:
		// EventThread, EventProcess, EventInitialized: no UI effect.
	}
	return nil
}
