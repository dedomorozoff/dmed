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

	"dmed/internal/config"
	"dmed/internal/dap"
	"dmed/internal/dbgp"
	"dmed/internal/syntax"
)

// debugBackend is the transport surface the debug panel needs: DAP adapters
// (*dap.Client) and the DBGp Xdebug client (*dbgp.Client) both implement it,
// so the panel, launch sequence and step keys work for either protocol.
type debugBackend interface {
	Initialize() (bool, error)
	Launch(map[string]interface{}) error
	ConfigureDone() error
	SetBreakpoints(path string, lines []int) ([]dap.Breakpoint, error)
	Continue(threadID int64) error
	Pause(threadID int64) error
	Next(threadID int64) error
	StepIn(threadID int64) error
	StepOut(threadID int64) error
	Threads() ([]dap.Thread, error)
	StackTrace(threadID int64, levels int) ([]dap.StackFrame, error)
	Scopes(frameID int64) ([]dap.Scope, error)
	Variables(ref int64) ([]dap.Variable, error)
	Evaluate(expr string, frameID int64) (dap.Variable, error)
	Close()
}

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
	cl       debugBackend
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

// dapLangPreset describes the [debug] adapter a language debugs with.
type dapLangPreset struct {
	adapter     string // adapter executable (connect/dbgp: display name only)
	adapterMode string // "reverse" | "stdio" | "connect" | "dbgp"
	adapterArgs string // connect/dbgp endpoint (host:port) or adapter CLI args
	mode        string // launch "mode" field; "" omits it (Xdebug etc. reject it)
	launchType  string // launch "type" field
	launchJSON  string // extra launch body merged over the computed one
}

// dapLangPresets maps chroma language tags (syntax.Lang) to their debug
// adapters. Go is the editor's default; PHP is debugged by spawning the
// interpreter with Xdebug, which dials back to us over DBGp. Add more
// languages here as they gain solid DAP/DBGp support.
var dapLangPresets = map[string]dapLangPreset{
	"go": {
		adapter:     "dlv",
		adapterMode: "reverse",
		mode:        "debug",
		launchType:  "go",
	},
	"php": {
		adapter:     "php",
		adapterMode: "dbgp",
		adapterArgs: "127.0.0.1:9003",
		launchType:  "php",
		launchJSON:  `{"type":"php","request":"launch"}`,
	},
	// JavaScript/TypeScript debug through vscode-js-debug's standalone stdio
	// adapter (node src/dap.js), the same layout VS Code itself uses. The
	// entry script is located at preset time (dapJsDebugScript); when it is
	// missing the session fails with an actionable error instead of spawning
	// a bare node process. The "type":"node" body is merged into the launch
	// request by dapLaunchArgs, together with program/cwd.
	"js": {
		adapter:     "node",
		adapterMode: "stdio",
		launchType:  "node",
		launchJSON:  `{"type":"node","request":"launch"}`,
	},
	"jsx": {
		adapter:     "node",
		adapterMode: "stdio",
		launchType:  "node",
		launchJSON:  `{"type":"node","request":"launch"}`,
	},
	"ts": {
		adapter:     "node",
		adapterMode: "stdio",
		launchType:  "node",
		launchJSON:  `{"type":"node","request":"launch"}`,
	},
}

// dapLangPreset deduces the [debug] settings for the active file's language.
// Adapter fields are applied only while they still carry dmed's built-in
// defaults (adapter_cmd=dlv/mode reverse/launch go/empty args+json, mode
// debug): any field pinned explicitly by the user wins over auto-detection.
// Returns ok=false when auto_detect is off, the file has no known language,
// or there is no preset for it.
func (m Model) dapLangPreset() (config.DebugConfig, bool) {
	if !m.cfg.Debug.AutoDetect {
		return config.DebugConfig{}, false
	}
	t := m.cur()
	if t == nil || t.path == "" {
		return config.DebugConfig{}, false
	}
	preset, ok := dapLangPresets[syntax.Lang(t.path)]
	if !ok {
		return config.DebugConfig{}, false
	}
	d := m.cfg.Debug
	if d.AdapterCmd == "" || d.AdapterCmd == "dlv" {
		d.AdapterCmd = preset.adapter
		d.AdapterMode = preset.adapterMode
		if d.AdapterArgs == "" {
			d.AdapterArgs = preset.adapterArgs
		}
		// The JS adapter is not a bare binary: it is `node <path to
		// vscode-js-debug's dap.js>`, so the script path is resolved at
		// preset time and becomes adapter_args. When the adapter is not
		// installed anywhere we know of, adapter_cmd falls back to plain
		// node and the start fails with an actionable error rather than a
		// bare interpreter that cannot speak DAP.
		if d.AdapterCmd == "node" && preset.adapter == "node" {
			script := dapJsDebugScript()
			if script == "" {
				d.AdapterCmd = ""
			} else {
				d.AdapterCmd = "node"
				d.AdapterArgs = script
			}
		}
	}
	if d.Mode == "debug" {
		d.Mode = preset.mode
	}
	if d.LaunchType == "go" {
		d.LaunchType = preset.launchType
	}
	if d.LaunchJSON == "" {
		d.LaunchJSON = preset.launchJSON
	}
	return d, true
}

// dapDebugCfg returns the [debug] settings for the live session: the
// language-auto-detected merge when one was computed at startDebugging, else
// the plain configured values.
func (m Model) dapDebugCfg() config.DebugConfig {
	if m.dapDeduced != nil {
		return *m.dapDeduced
	}
	return m.cfg.Debug
}

// dapJsDebugScript locates vscode-js-debug's standalone stdio adapter script
// (src/dap.js). It is searched in the same places VS Code installs it — the
// user's extension directories for stable/insiders/VSCodium and the remote
// server layout — and can be pinned with DMED_JS_DEBUG. The path is passed to
// `node` as the adapter command's argument; npm-installed copies
// (`npm i @vscode/js-debug-adapter`) land in node_modules and are found via
// the PATH-independent node_modules scan.
func dapJsDebugScript() string {
	if p := strings.TrimSpace(os.Getenv("DMED_JS_DEBUG")); p != "" {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	extDirs := []string{
		filepath.Join(home, ".vscode", "extensions"),
		filepath.Join(home, ".vscode-insiders", "extensions"),
		filepath.Join(home, ".vscodium", "extensions"),
		filepath.Join(home, ".cursor", "extensions"),
		filepath.Join(home, ".vscode-server", "extensions"),
	}
	for _, base := range extDirs {
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), "ms-vscode.js-debug") {
				continue
			}
			candidate := filepath.Join(base, e.Name(), "src", "dap.js")
			if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
				return candidate
			}
		}
	}
	for _, cand := range []string{
		filepath.Join(home, "node_modules", "@vscode", "js-debug-adapter", "src", "dap.js"),
		filepath.Join(home, "node_modules", "@vscode", "js-debug-adapter", "vscode-js-debug", "src", "dap.js"),
	} {
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return cand
		}
	}
	return ""
}

// dapJsDebugError explains why a JS/TS debug session failed when the
// vscode-js-debug adapter could not be located (the preset left adapter_cmd
// empty on purpose so this branch can fire).
func dapJsDebugError() error {
	return fmt.Errorf("debug: vscode-js-debug adapter not found — install the \"JavaScript Debugger\" VS Code extension, `npm i -g @vscode/js-debug-adapter`, or point [debug] adapter_cmd at `node` and adapter_args at its src/dap.js (or set DMED_JS_DEBUG)")
}

// dapStartCmd spawns the DAP adapter and completes the initialize handshake
// in the background so the UI never blocks on adapter startup. The outcome
// arrives as a dapStartMsg stamped with the session generation; results from
// superseded sessions are dropped by the Update handler.
func (m *Model) dapStartCmd() tea.Cmd {
	dc := m.dapDebugCfg()
	adapter := dc.AdapterCmd
	if adapter == "" {
		switch dc.AdapterMode {
		case "connect":
			adapter = "xdebug" // connect mode spawns nothing; display name only
		case "dbgp":
			adapter = "php"
		default:
			adapter = "dlv"
		}
	}
	mode := dc.AdapterMode
	if mode == "" {
		mode = "reverse"
	}
	isConnect := mode == "connect"
	isDBGP := mode == "dbgp"
	// The deduced JS preset leaves adapter_cmd empty on purpose when the
	// vscode-js-debug adapter script is not installed anywhere we know of, so
	// the user gets an actionable error instead of a bare node process that
	// cannot speak DAP.
	if adapter == "" && !isConnect && !isDBGP {
		return func() tea.Msg { return dapStartMsg{gen: m.dapGen, err: dapJsDebugError()} }
	}
	// In connect/dbgp mode adapter_args is the endpoint address, not CLI
	// arguments for an adapter process, so skip the Delve/stdio splitting.
	cmdAdapter, adapterArgs := adapter, []string(nil)
	if !isConnect && !isDBGP {
		cmdAdapter, adapterArgs = dapAdapterCommand(adapter, dc.AdapterArgs)
	}
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
		if mode == "stdio" || mode == "reverse" {
			if _, err := exec.LookPath(cmdAdapter); err != nil {
				return dapStartMsg{gen: gen, err: fmt.Errorf("debug: %s not found — install it or set [debug] adapter_cmd", cmdAdapter)}
			}
		}
		onEvent := func(e dap.Event) {
			select {
			case ch <- dapEventMsg{ev: e, gen: gen}:
			default: // drop if the UI is backed up
			}
		}
		var (
			cl  debugBackend
			err error
		)
		switch {
		case mode == "stdio":
			cl, err = dap.StartStdio(cmdAdapter, adapterArgs, root, onEvent)
		case isConnect:
			addr := dc.AdapterArgs
			if addr == "" {
				addr = "127.0.0.1:9003" // Xdebug's conventional endpoint
			}
			cl, err = dap.StartConnect(addr, root, onEvent)
		case isDBGP:
			addr := dc.AdapterArgs
			if addr == "" {
				addr = "127.0.0.1:9003" // Xdebug's connect-back port
			}
			program := dc.Program
			if program == "" {
				program = m.dapProgram()
			}
			cl, err = dbgp.StartDebugger(cmdAdapter, program,
				strings.Fields(dc.Args), root, addr, onEvent)
		default:
			cl, err = dap.StartReverse(cmdAdapter, adapterArgs, root, onEvent)
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
	dc := m.dapDebugCfg()
	args := map[string]interface{}{
		"request": dc.LaunchRequest,
		"type":    dc.LaunchType,
	}
	if dc.Mode != "" {
		args["mode"] = dc.Mode
	}
	if p := m.dapProgram(); p != "" {
		args["program"] = p
	}
	if c := m.dapCwd(); c != "" {
		args["cwd"] = c
	}
	if dc.StopOnEntry {
		args["stopOnEntry"] = true
	}
	if a := strings.Fields(dc.Args); len(a) > 0 {
		args["args"] = a
	}
	if dc.LaunchJSON == "" {
		return args, nil
	}
	var extra map[string]interface{}
	if err := json.Unmarshal([]byte(dc.LaunchJSON), &extra); err != nil {
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
// Script languages (PHP/Xdebug, Node via js-debug) debug the active file,
// not a directory.
func (m Model) dapProgram() string {
	if p := m.dapDebugCfg().Program; p != "" {
		return p
	}
	if t := m.cur(); t != nil && t.path != "" {
		if filepath.Ext(t.path) == ".php" {
			return t.path
		}
		switch syntax.Lang(t.path) {
		case "js", "jsx", "ts":
			// Node runs an entry file, not a package directory; only
			// fall back to the directory when the tab is a folder.
			if st, err := os.Stat(t.path); err == nil && !st.IsDir() {
				return t.path
			}
		}
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
	if dc, ok := m.dapLangPreset(); ok {
		m.dapDeduced = &dc
	}
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
	m.dapDeduced = nil // drop the language-detected config with the session
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
			m.dapClearConsole()
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
		if m.dapConsolePeek {
			m.dapScrollConsole(1) // the peek has no selection: walk the backlog
			return nil
		}
		return m.dapMoveSelCmd(-1)
	case "down", "j":
		if m.dapConsolePeek {
			m.dapScrollConsole(-1)
			return nil
		}
		return m.dapMoveSelCmd(1)
	case "pgup":
		if m.dapConsolePeek {
			m.dapScrollConsole(m.dapListRows())
			return nil
		}
		return m.dapMoveSelPageCmd(-1)
	case "pgdown":
		if m.dapConsolePeek {
			m.dapScrollConsole(-m.dapListRows())
			return nil
		}
		return m.dapMoveSelPageCmd(1)
	case "home":
		if m.dapConsolePeek {
			m.dapConsoleToEdge(true)
			return nil
		}
		return m.dapMoveSelEdgeCmd(true)
	case "end":
		if m.dapConsolePeek {
			m.dapConsoleToEdge(false)
			return nil
		}
		return m.dapMoveSelEdgeCmd(false)
	case "+", "=":
		m.growDapPanel(1)
		return nil
	case "-", "_":
		m.growDapPanel(-1)
		return nil
	case "ctrl+l":
		m.dapClearConsole()
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

// dapSelList returns the current index into, and the length of, the focused
// list.
func (m Model) dapSelList() (idx, total int) {
	switch m.dapFocus {
	case 0:
		return m.dapSelThread, len(m.dapThreads)
	case 1:
		return m.dapSelFrame, len(m.dapFrames)
	default:
		return m.dapVarSel, len(m.dapCurrentVars())
	}
}

// dapSetSel writes a selection index into the focused list, clamped to it. An
// empty list keeps index 0, which is what the columns render an empty state
// from.
func (m *Model) dapSetSel(i int) {
	_, total := m.dapSelList()
	if total == 0 {
		i = 0
	} else {
		i = maxInt(i, 0)
		if i > total-1 {
			i = total - 1
		}
	}
	switch m.dapFocus {
	case 0:
		m.dapSelThread = i
	case 1:
		m.dapSelFrame = i
	default:
		m.dapVarSel = i
	}
}

// dapMoveSelCmd moves the focused list's selection by one entry, wrapping at
// the ends.
func (m *Model) dapMoveSelCmd(d int) tea.Cmd {
	idx, total := m.dapSelList()
	return m.dapSetSelCmd((idx + d + total) % maxInt(total, 1))
}

// dapMoveSelPageCmd jumps one screenful of the focused list. Unlike a one-step
// move it clamps instead of wrapping: a page step that wrapped would throw the
// selection to the far end of a long stack and lose the place.
func (m *Model) dapMoveSelPageCmd(dir int) tea.Cmd {
	idx, _ := m.dapSelList()
	return m.dapSetSelCmd(idx + dir*m.dapListRows())
}

// dapMoveSelEdgeCmd jumps to the top (first) or bottom (last) entry.
func (m *Model) dapMoveSelEdgeCmd(top bool) tea.Cmd {
	_, total := m.dapSelList()
	if top {
		return m.dapSetSelCmd(0)
	}
	return m.dapSetSelCmd(total - 1)
}

// dapSetSelCmd applies idx to the focused list and reloads the snapshot when
// the move changes what the other columns show: another thread carries its own
// stack, another frame its own variables. Selecting a thread or frame used to
// leave the stack/variables pane on stale data.
func (m *Model) dapSetSelCmd(idx int) tea.Cmd {
	switch m.dapFocus {
	case 0:
		if len(m.dapThreads) == 0 {
			return nil
		}
		before := m.dapSelThread
		m.dapSetSel(idx)
		if m.dapSelThread == before {
			return nil
		}
		return m.dapRefreshCmd()
	case 1:
		if len(m.dapFrames) == 0 {
			return nil
		}
		before := m.dapSelFrame
		m.dapSetSel(idx)
		if m.dapSelFrame == before {
			return nil
		}
		return m.dapRefreshCmd()
	}
	m.dapSetSel(idx)
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
// panel state. The stopped location is always re-derived from the selected
// stack frame so the ▶ marker survives even when scopes/variables requests
// fail. It reports whether the follow reveal is due (this refresh moved the
// stopped location or an earlier stop armed the flag).
func (m *Model) applyDAPRefresh(msg dapRefreshMsg) bool {
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
		m.updateDapCurrentLocationFollow()
		return m.dapFollowPending
	}
	if msg.scopePush {
		m.dapVarStack = append(m.dapVarStack, msg.vars)
	} else {
		m.dapVarStack = [][]dapVarRow{msg.vars}
	}
	m.dapVarSel = 0
	m.updateDapCurrentLocationFollow()
	return m.dapFollowPending
}

// handleDapFollowMsg reveals the stopped location in the editor: opens the
// file, puts the cursor on the stopped line and scrolls it into view.
func (m *Model) handleDapFollowMsg() tea.Cmd {
	m.dapFollowPending = false
	if m.dapRunState != dapStopped || m.dapCurPath == "" || m.dapCurLine <= 0 {
		return nil
	}
	m.focusOrOpen(m.dapCurPath)
	if t := m.cur(); t != nil {
		t.buf.SetCursor(m.dapCurLine-1, 0)
		t.buf.Deselect()
		m.clampScroll()
	}
	return nil
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
// It reports whether the location actually moved (a fresh stop or a different
// frame was picked), so the caller can reveal that spot in the editor.
func (m *Model) updateDapCurrentLocation() bool {
	if len(m.dapFrames) > 0 && m.dapSelFrame < len(m.dapFrames) {
		f := m.dapFrames[m.dapSelFrame]
		if f.Path != "" {
			if f.Path != m.dapCurPath || f.Line != m.dapCurLine {
				m.dapCurPath = f.Path
				m.dapCurLine = f.Line
				return true
			}
			return false
		}
	}
	return false
}

// updateDapCurrentLocationFollow wraps updateDapCurrentLocation, arming the
// follow-the-debuggee reveal when the stopped location moved.
func (m *Model) updateDapCurrentLocationFollow() {
	if m.updateDapCurrentLocation() {
		m.dapFollowPending = true
	}
}

// dapFollowMsg asks Update to reveal the stopped location (open the file,
// put the cursor on the stopped line) on the model that consumes it, not on
// the value snapshot that produced the refresh.
type dapFollowMsg struct{}

// dapFollowCmd schedules the reveal as a command.
func (m *Model) dapFollowCmd() tea.Cmd {
	return func() tea.Msg {
		return dapFollowMsg{}
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
		// The reveal (open file, cursor on the stop line) is deferred to the
		// follow msg so it lands on the model that is current when it runs,
		// not on the value snapshot inside this event handler.
		if m.dapCurPath != "" && m.dapCurLine > 0 {
			m.dapFollowPending = true
		}
		m.dapOpen = true
		m.termOpen = false
		return tea.Batch(m.dapFollowCmd(), m.dapRefreshCmd())
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
		m.dapAppendConsole(line)
	case dap.EventExited:
		m.dapAppendConsole(fmt.Sprintf("process exited with code %d", ev.ExitCode))
		m.msg = fmt.Sprintf("debug: process exited with code %d", ev.ExitCode)
	case dap.EventTerminated:
		m.dapRunState = dapEnded
		m.dapCurPath = ""
		m.dapCurLine = 0
		m.dapAppendConsole("debug session terminated")
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
