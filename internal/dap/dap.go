// Package dap implements a Debug Adapter Protocol (DAP) client over
// Content-Length framed JSON-RPC 2.0, the same wire transport used by LSP.
// It is transport-agnostic at the core (any io.ReadWriteCloser) with
// ready-made launchers for stdio adapters and for Delve's reverse-connect
// mode (`dlv dap --client-addr`), which the editor targets for Go.
package dap

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dmed/internal/debug"
)

// EventKind enumerates the DAP server -> client events the editor consumes.
type EventKind int

const (
	EventInitialized EventKind = iota
	EventStopped
	EventContinued
	EventOutput
	EventExited
	EventTerminated
	EventBreakpoint
	EventThread
	EventProcess
	EventDisconnected
)

// Event is a decoded DAP server event. Only the fields relevant to its kind
// are populated.
type Event struct {
	Kind        EventKind
	Reason      string
	ThreadID    int64
	OutputCat   string // "stdout" | "stderr" | "console" | ...
	Output      string
	SourcePath  string
	SourceName  string
	ExitCode    int
	BPTitle     string
	BPVerified  bool
	BPID        int64
	Line        int
	ProcessID   int
	ProcessName string
}

// Thread is a debuggee thread from the threads response.
type Thread struct {
	ID   int64
	Name string
}

// StackFrame is one entry of the threads stack trace.
type StackFrame struct {
	ID   int64
	Name string
	Path string
	Line int // 1-based
	Col  int // 1-based
}

// Scope is a variables scope (locals, arguments, globals, ...) of a frame.
type Scope struct {
	Name         string
	VariablesRef int64
	Expensive    bool
}

// Variable is a debuggee variable or a child of one.
type Variable struct {
	Name         string
	Value        string
	VariablesRef int64
	Type         string
}

// Breakpoint is the adapter's confirmation for a source breakpoint request.
type Breakpoint struct {
	Verified bool
	Line     int
	Message  string
}

// OnEvent is invoked from the read loop for every decoded server event. It
// must not issue DAP requests: the read loop is single-threaded and would
// deadlock waiting for responses.
type OnEvent func(e Event)

// Client is a DAP client over a single read/write stream (`io.ReadWriteCloser`
// works for both stdio pipes and TCP connections).
type Client struct {
	conn    io.ReadWriteCloser
	nextID  int64
	mu      sync.Mutex
	pending map[int64]chan *callResult
	onEvent OnEvent
	adapter *exec.Cmd
	listen  net.Listener
	closed  int32
}

// callResult is the outcome of one request delivered to its waiter.
type callResult struct {
	body json.RawMessage
	err  error
}

type rpcRequest struct {
	Type    string      `json:"type"`
	Seq     int64       `json:"seq"`
	Command string      `json:"command"`
	Args    interface{} `json:"arguments,omitempty"`
}

// DAP messages look like JSON-RPC but carry "type"/"seq" instead of
// "jsonrpc"/"id"/"method". Decode both shapes: responses carry id/method the
// same way as LSP does, so reuse the LSP reader layout with aliased tags.
type rpcResponse struct {
	Type       string          `json:"type"`
	Seq        int64           `json:"seq"`
	RequestSeq int64           `json:"request_seq"`
	Success    bool            `json:"success"`
	Command    string          `json:"command"`
	Message    string          `json:"message"`
	Body       json.RawMessage `json:"body,omitempty"`
	Arguments  json.RawMessage `json:"arguments,omitempty"`
	Event      string          `json:"event,omitempty"`
}

// NewClient wraps an existing stream. The stream is owned by the client and
// closed on Close. The read loop is started immediately.
func NewClient(conn io.ReadWriteCloser, onEvent OnEvent) *Client {
	c := &Client{
		conn:    conn,
		pending: make(map[int64]chan *callResult),
		onEvent: onEvent,
	}
	go debug.CapturePanicReport(c.readLoop)
	return c
}

// StdioConn adapts command stdin/stdout pipes into a single io.ReadWriteCloser.
type StdioConn struct {
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
}

func (s *StdioConn) Read(p []byte) (int, error)  { return s.Stdout.Read(p) }
func (s *StdioConn) Write(p []byte) (int, error) { return s.Stdin.Write(p) }
func (s *StdioConn) Close() error {
	err1 := s.Stdout.Close()
	err2 := s.Stdin.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

// StartStdio launches an adapter as a child process speaking DAP over stdin/
// stdout (the classic LSP-style layout used by most adapters: debugpy,
// lldb-dap, codelldb, ...). rootDir is the working directory of the adapter
// process. Adapter stderr is surfaced as "console" output events.
func StartStdio(adapter string, args []string, rootDir string, onEvent OnEvent) (*Client, error) {
	cmd := exec.Command(adapter, args...)
	cmd.Dir = rootDir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, err
	}
	go debug.CapturePanicReport(func() {
		// Surface adapter diagnostics as console lines so the user sees them
		// without leaving the editor. stdout is DAP traffic, so only scrape
		// stderr here.
		_ = outputScanner(stderr, func(line string) {
			if onEvent != nil {
				onEvent(Event{Kind: EventOutput, OutputCat: "console", Output: line})
			}
		})
	})
	c := NewClient(&StdioConn{Stdin: stdin, Stdout: stdout}, onEvent)
	c.adapter = cmd
	return c, nil
}

// StartReverse launches an adapter in Delve's reverse-connect mode: dmed
// listens on an ephemeral localhost port and passes `--client-addr=127.0.0.1:P`
// so the adapter dials us instead of us having to scrape its stdout for a
// port. Progress is delivered as output events with category "console".
func StartReverse(adapter string, args []string, rootDir string, onEvent OnEvent) (*Client, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("dap listen: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	clientAddr := "127.0.0.1:" + strconv.Itoa(port)

	// No explicit --client-addr? Append one so the adapter dials us back.
	has := false
	for _, a := range args {
		if strings.HasPrefix(a, "--client-addr=") {
			has = true
			break
		}
	}
	fullArgs := args
	if !has {
		fullArgs = append(append([]string{}, args...), "--client-addr="+clientAddr)
	}

	cmd := exec.Command(adapter, fullArgs...)
	cmd.Dir = rootDir
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	go debug.CapturePanicReport(func() {
		// Surface adapter diagnostics (missing tools, build failures) as
		// console lines so the user sees them without leaving the editor.
		_ = outputScanner(stderr, func(line string) {
			if onEvent != nil {
				onEvent(Event{Kind: EventOutput, OutputCat: "console", Output: line})
			}
		})
	})
	if err := cmd.Start(); err != nil {
		_ = ln.Close()
		_ = stderr.Close()
		return nil, err
	}

	// Wait for the adapter to dial in. The accept must complete quickly
	// (Delve connects immediately on startup); time out so a dead adapter
	// never leaves the editor hanging.
	type accepted struct {
		conn net.Conn
		err  error
	}
	acceptCh := make(chan accepted, 1)
	go debug.CapturePanicReport(func() {
		c, err := ln.Accept()
		if err != nil {
			acceptCh <- accepted{err: err}
			return
		}
		acceptCh <- accepted{conn: c}
	})
	select {
	case a := <-acceptCh:
		if a.err != nil {
			_ = ln.Close()
			_ = cmd.Process.Kill()
			return nil, fmt.Errorf("dap accept: %w", a.err)
		}
		_ = ln.Close() // single client; release the listener
		c := NewClient(a.conn, onEvent)
		c.adapter = cmd
		c.listen = ln
		return c, nil
	case <-time.After(15 * time.Second):
		_ = ln.Close()
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("dap: adapter %s did not connect", adapter)
	}
}

func outputScanner(r io.Reader, emit func(string)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		emit(sc.Text())
	}
	return sc.Err()
}

// Seq returns the next outbound sequence number (also used as the request id).
func (c *Client) Seq() int64 {
	return atomic.AddInt64(&c.nextID, 1)
}

const callTimeout = 60 * time.Second

func (c *Client) call(command string, args interface{}) (json.RawMessage, error) {
	seq := c.Seq()
	ch := make(chan *callResult, 1)

	c.mu.Lock()
	c.pending[seq] = ch
	c.mu.Unlock()

	req := rpcRequest{Type: "request", Seq: seq, Command: command, Args: args}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if err := c.send(data); err != nil {
		// The write failed (broken pipe / dead adapter): unblock the waiter.
		c.mu.Lock()
		if w, ok := c.pending[seq]; ok {
			delete(c.pending, seq)
			w <- &callResult{err: err}
		}
		c.mu.Unlock()
		return nil, err
	}

	select {
	case res := <-ch:
		return res.body, res.err
	case <-time.After(callTimeout):
		return nil, fmt.Errorf("dap: %s timed out", command)
	}
}

func (c *Client) notify(command string, args interface{}) error {
	req := rpcRequest{Type: "request", Seq: c.Seq(), Command: command, Args: args}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return c.send(data)
}

func (c *Client) send(data []byte) error {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	c.mu.Lock()
	_, err := c.conn.Write(append([]byte(header), data...))
	c.mu.Unlock()
	return err
}

func (c *Client) readLoop() {
	r := bufio.NewReader(c.conn)
	for {
		contentLength := 0
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				c.disconnected()
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if strings.HasPrefix(strings.ToLower(line), "content-length:") {
				parts := strings.Split(line, ":")
				if len(parts) >= 2 {
					contentLength, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
				}
			}
		}
		if contentLength <= 0 {
			continue
		}
		body := make([]byte, contentLength)
		if _, err := io.ReadFull(r, body); err != nil {
			c.disconnected()
			return
		}

		var msg rpcResponse
		if err := json.Unmarshal(body, &msg); err != nil {
			continue
		}
		if msg.Type == "response" {
			c.mu.Lock()
			ch, ok := c.pending[msg.RequestSeq]
			if ok {
				delete(c.pending, msg.RequestSeq)
			}
			c.mu.Unlock()
			if !ok {
				// Response for an already-timed-out request; ignore.
				continue
			}
			if msg.Success {
				ch <- &callResult{body: msg.Body}
			} else {
				if msg.Message == "" {
					msg.Message = "unknown error"
				}
				ch <- &callResult{err: fmt.Errorf("dap %s: %s", msg.Command, msg.Message)}
			}
		} else if msg.Type == "event" {
			c.handleEvent(msg.Event, msg.Body)
		}
	}
}

func (c *Client) disconnected() {
	// The adapter went away: fail every in-flight request instead of leaving
	// its waiter sitting on a 60s timeout.
	c.mu.Lock()
	for seq, ch := range c.pending {
		delete(c.pending, seq)
		ch <- &callResult{err: fmt.Errorf("dap: connection closed")}
	}
	c.mu.Unlock()
	if c.onEvent != nil {
		c.onEvent(Event{Kind: EventDisconnected})
	}
}

func (c *Client) handleEvent(name string, body json.RawMessage) {
	if c.onEvent == nil {
		return
	}
	events := []Event{}
	defer func() {
		for _, e := range events {
			c.onEvent(e)
		}
	}()
	switch name {
	case "initialized":
		events = append(events, Event{Kind: EventInitialized})
	case "stopped":
		var p struct {
			Reason            string  `json:"reason"`
			ThreadID          int64   `json:"threadId"`
			AllThreadsStopped bool    `json:"allThreadsStopped"`
			HitBreakpointIDs  []int64 `json:"hitBreakpointIds"`
			Line              int     `json:"line"`
			SourcePath        string  `json:"path"`
		}
		_ = json.Unmarshal(body, &p)
		events = append(events, Event{
			Kind: EventStopped, Reason: p.Reason, ThreadID: p.ThreadID,
			SourcePath: p.SourcePath, Line: p.Line,
		})
	case "continued":
		var p struct {
			ThreadID int64 `json:"threadId"`
		}
		_ = json.Unmarshal(body, &p)
		events = append(events, Event{Kind: EventContinued, ThreadID: p.ThreadID})
	case "output":
		var p struct {
			Category string `json:"category"`
			Output   string `json:"output"`
			Source   struct {
				Name string `json:"name"`
				Path string `json:"path"`
			} `json:"source"`
		}
		_ = json.Unmarshal(body, &p)
		events = append(events, Event{
			Kind: EventOutput, OutputCat: p.Category, Output: p.Output,
			SourceName: p.Source.Name, SourcePath: p.Source.Path,
		})
	case "exited":
		var p struct {
			ExitCode int `json:"exitCode"`
		}
		_ = json.Unmarshal(body, &p)
		events = append(events, Event{Kind: EventExited, ExitCode: p.ExitCode})
	case "terminated":
		var p struct {
			Restart json.RawMessage `json:"restart"`
		}
		_ = json.Unmarshal(body, &p)
		events = append(events, Event{Kind: EventTerminated})
	case "breakpoint":
		var p struct {
			Reason string `json:"reason"`
			BP     struct {
				ID       int64  `json:"id"`
				Verified bool   `json:"verified"`
				Line     int    `json:"line"`
				Message  string `json:"message"`
				Source   struct {
					Name string `json:"name"`
					Path string `json:"path"`
				} `json:"source"`
			} `json:"breakpoint"`
		}
		_ = json.Unmarshal(body, &p)
		events = append(events, Event{
			Kind: EventBreakpoint, BPTitle: p.Reason, BPID: p.BP.ID,
			BPVerified: p.BP.Verified, Line: p.BP.Line,
			SourcePath: p.BP.Source.Path, SourceName: p.BP.Source.Name,
		})
	case "thread":
		var p struct {
			Reason   string `json:"reason"`
			ThreadID int64  `json:"threadId"`
		}
		_ = json.Unmarshal(body, &p)
		events = append(events, Event{Kind: EventThread, Reason: p.Reason, ThreadID: p.ThreadID})
	case "process":
		var p struct {
			Name            string `json:"name"`
			SystemProcessID int    `json:"systemProcessId"`
		}
		_ = json.Unmarshal(body, &p)
		events = append(events, Event{Kind: EventProcess, ProcessName: p.Name, ProcessID: p.SystemProcessID})
	default:
		// Unknown events are ignored (module, loadedSource, etc.).
	}
}

// Close disconnects the session and tears down the adapter process.
func (c *Client) Close() {
	if atomic.AddInt32(&c.closed, 1) > 1 {
		return
	}
	_ = c.notify("disconnect", map[string]interface{}{"terminateDebuggee": true})
	_ = c.conn.Close()
	if c.adapter != nil && c.adapter.Process != nil {
		_ = c.adapter.Process.Kill()
	}
	if c.listen != nil {
		_ = c.listen.Close()
	}
}

// Initialize answers the adapter's capability handshake, then prepares for the
// session. The response's supportsConfigurationDoneRequest is remembered so
// ConfigureDone only runs for adapters that need it.
func (c *Client) Initialize() (supportsConfigDone bool, err error) {
	res, err := c.call("initialize", map[string]interface{}{
		"clientID":                 "dmed",
		"adapterID":                "dmed",
		"linesStartAt1":            true,
		"columnsStartAt1":          true,
		"pathFormat":               "path",
		"supportsVariablePaging":   true,
		"supportsMemoryReferences": true,
	})
	if err != nil {
		return false, err
	}
	var caps struct {
		SupportsConfigurationDoneRequest bool `json:"supportsConfigurationDoneRequest"`
	}
	_ = json.Unmarshal(res, &caps)
	return caps.SupportsConfigurationDoneRequest, nil
}

// Launch starts (or attaches to) a debuggee. The arguments body is entirely
// adapter-specific — for Delve it is the classic `type: "go", mode, program,
// args, cwd, stopOnEntry` shape; other adapters use their own keys. The
// editor composes the body from [debug] config (see internal/config).
func (c *Client) Launch(args map[string]interface{}) error {
	_, err := c.call("launch", args)
	return err
}

// ConfigureDone tells the adapter the client finished its start-up requests.
func (c *Client) ConfigureDone() error {
	_, err := c.call("configurationDone", map[string]interface{}{})
	return err
}

// SetBreakpoints installs breakpoints at the given (1-based) lines of a source
// file and returns the adapter's verification results.
func (c *Client) SetBreakpoints(path string, lines []int) ([]Breakpoint, error) {
	bps := make([]map[string]interface{}, 0, len(lines))
	for _, l := range lines {
		bps = append(bps, map[string]interface{}{"line": l})
	}
	res, err := c.call("setBreakpoints", map[string]interface{}{
		"source":      map[string]interface{}{"path": path},
		"breakpoints": bps,
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Breakpoints []struct {
			Verified bool   `json:"verified"`
			Line     int    `json:"line"`
			Message  string `json:"message"`
		} `json:"breakpoints"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(res, &out); err == nil && out.Breakpoints != nil {
		body := make([]Breakpoint, 0, len(out.Breakpoints))
		for _, b := range out.Breakpoints {
			body = append(body, Breakpoint{Verified: b.Verified, Line: b.Line, Message: b.Message})
		}
		return body, nil
	}
	if out.Error != "" {
		return nil, fmt.Errorf("setBreakpoints: %s", out.Error)
	}
	return []Breakpoint{}, nil
}

// Continue resumes the stopped thread.
func (c *Client) Continue(threadID int64) error {
	_, err := c.call("continue", map[string]interface{}{"threadId": threadID})
	return err
}

// Next steps over one line. StepIn steps into, StepOut steps out.
func (c *Client) Next(threadID int64) error {
	_, err := c.call("next", map[string]interface{}{"threadId": threadID})
	return err
}

func (c *Client) StepIn(threadID int64) error {
	_, err := c.call("stepIn", map[string]interface{}{"threadId": threadID})
	return err
}

func (c *Client) StepOut(threadID int64) error {
	_, err := c.call("stepOut", map[string]interface{}{"threadId": threadID})
	return err
}

// Threads lists the running threads.
func (c *Client) Threads() ([]Thread, error) {
	res, err := c.call("threads", map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	var out struct {
		Threads []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"threads"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(res, &out); err == nil {
		var ts []Thread
		for _, t := range out.Threads {
			ts = append(ts, Thread{ID: t.ID, Name: t.Name})
		}
		return ts, nil
	}
	if out.Error != "" {
		return nil, fmt.Errorf("threads: %s", out.Error)
	}
	return nil, fmt.Errorf("threads: unexpected response")
}

// StackTrace returns the call stack of a thread, frames listed top-first.
func (c *Client) StackTrace(threadID int64, levels int) ([]StackFrame, error) {
	res, err := c.call("stackTrace", map[string]interface{}{
		"threadId":   threadID,
		"startFrame": 0,
		"levels":     levels,
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		StackFrames []struct {
			ID     int64  `json:"id"`
			Name   string `json:"name"`
			Line   int    `json:"line"`
			Column int    `json:"column"`
			Source struct {
				Name string `json:"name"`
				Path string `json:"path"`
			} `json:"source"`
		} `json:"stackFrames"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(res, &out); err == nil {
		var fs []StackFrame
		for _, f := range out.StackFrames {
			fs = append(fs, StackFrame{
				ID: f.ID, Name: f.Name, Path: f.Source.Path, Line: f.Line, Col: f.Column,
			})
		}
		return fs, nil
	}
	if out.Error != "" {
		return nil, fmt.Errorf("stackTrace: %s", out.Error)
	}
	return nil, fmt.Errorf("stackTrace: unexpected response")
}

// Scopes lists the variable scopes of a stack frame.
func (c *Client) Scopes(frameID int64) ([]Scope, error) {
	res, err := c.call("scopes", map[string]interface{}{"frameId": frameID})
	if err != nil {
		return nil, err
	}
	var out struct {
		Scopes []struct {
			Name         string `json:"name"`
			VariablesRef int64  `json:"variablesReference"`
			Expensive    bool   `json:"expensive"`
		} `json:"scopes"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(res, &out); err == nil {
		var sc []Scope
		for _, s := range out.Scopes {
			sc = append(sc, Scope{Name: s.Name, VariablesRef: s.VariablesRef, Expensive: s.Expensive})
		}
		return sc, nil
	}
	if out.Error != "" {
		return nil, fmt.Errorf("scopes: %s", out.Error)
	}
	return nil, fmt.Errorf("scopes: unexpected response")
}

// Variables lists the children of a variables reference (0 references the
// root — callers should not pass 0).
func (c *Client) Variables(ref int64) ([]Variable, error) {
	res, err := c.call("variables", map[string]interface{}{
		"variablesReference": ref,
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Variables []struct {
			Name         string `json:"name"`
			Value        string `json:"value"`
			VariablesRef int64  `json:"variablesReference"`
			Type         string `json:"type"`
		} `json:"variables"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(res, &out); err == nil {
		var vs []Variable
		for _, v := range out.Variables {
			vs = append(vs, Variable{
				Name: v.Name, Value: v.Value, VariablesRef: v.VariablesRef, Type: v.Type,
			})
		}
		return vs, nil
	}
	if out.Error != "" {
		return nil, fmt.Errorf("variables: %s", out.Error)
	}
	return nil, nil
}

// Evaluate evaluates an expression against a stack frame (or the global scope
// when frameID is negative).
func (c *Client) Evaluate(expr string, frameID int64) (Variable, error) {
	args := map[string]interface{}{"expression": expr, "context": "repl"}
	if frameID >= 0 {
		args["frameId"] = frameID
	}
	res, err := c.call("evaluate", args)
	if err != nil {
		return Variable{}, err
	}
	var out struct {
		Result       string `json:"result"`
		VariablesRef int64  `json:"variablesReference"`
		Type         string `json:"type"`
		Error        string `json:"error"`
	}
	_ = json.Unmarshal(res, &out)
	if out.Error != "" {
		return Variable{}, fmt.Errorf("evaluate: %s", out.Error)
	}
	return Variable{Name: expr, Value: out.Result, VariablesRef: out.VariablesRef, Type: out.Type}, nil
}
