// Package mcp implements a minimal Model Context Protocol client over the
// stdio transport: JSON-RPC 2.0 messages, one per line, spoken to an external
// tool server process.
//
// The client is deliberately small. It implements the client side of the
// 2025-03-26 protocol: the initialize handshake, tools/list and tools/call.
// It advertises no capabilities, so a server request to the client (sampling,
// roots) is answered with a method-not-found error rather than left hanging —
// servers treat the refusal as "the client cannot do that" and continue.
//
// Framing note: the stdio transport is newline-delimited JSON, *not* the
// Content-Length framing LSP and DAP use. One message per line, embedded
// newlines escaped by encoding/json; that is the whole of it.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
)

// ProtocolVersion is the MCP revision this client speaks.
const ProtocolVersion = "2025-03-26"

// maxLineLen bounds one inbound message; a tool result can be large, and a
// scanner that refuses it would silently kill the session.
const maxLineLen = 16 << 20

// ErrDead reports a server whose pipe or process ended.
var ErrDead = errors.New("mcp: server is not running")

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("mcp error %d: %s", e.Code, e.Message) }

// frame is one JSON-RPC 2.0 message in any direction: request, response or
// notification. An ID that is present marks request/response pairs; the
// client only ever sends numeric ids and servers must echo them back.
type frame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// ServerInfo is what the initialize handshake reported about the server.
type ServerInfo struct {
	Name       string
	Version    string
	Protocol   string
	Capability []string // advertised server capability names (tools, ...)
}

// Tool is one tool a server exposes. InputSchema is the raw JSON Schema the
// server declared; nil means the server sent none.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// ToolOutcome is the content of one tools/call response flattened to text.
type ToolOutcome struct {
	Text    string
	IsError bool
}

// Client speaks MCP over an existing pair of pipes. The read side runs on its
// own goroutine; requests are correlated by id and woken on the response.
type Client struct {
	w    io.Writer
	wmu  sync.Mutex // one write at a time; the frame must land as one line
	pmu  sync.Mutex
	next int64
	pend map[int64]chan frame
	dead error // set once by the read loop when the stream ends

	in     io.Reader
	cmd    *exec.Cmd // non-nil when Spawn started the process
	kick   func()    // interrupt stdin so the server exits
	stderr *capped   // the server's error output, for death reports

	alive atomic.Bool
}

// New attaches a client to an already-open stream (in = the server's stdout,
// w = the server's stdin). kick, may be nil, is asked to end the session when
// the client is closed. Tests drive the protocol over net.Pipe with this.
func New(in io.Reader, w io.Writer, kick func()) *Client {
	c := &Client{in: in, w: w, pend: map[int64]chan frame{}, kick: kick}
	c.alive.Store(true)
	go c.readLoop()
	return c
}

// Spawn starts cmdline in dir and attaches to its stdin/stdout. The command
// is split on whitespace; on Windows a resolved .cmd/.bat is run through
// cmd.exe, which is how npx-launched MCP servers start here.
func Spawn(cmdline, dir string) (*Client, error) {
	fields := strings.Fields(cmdline)
	if len(fields) == 0 {
		return nil, errors.New("mcp: empty server command")
	}
	var argv []string
	if runtime.GOOS == "windows" {
		// A server launched as npx lands on npx.cmd, which CreateProcess
		// cannot run directly; cmd.exe /c can.
		if p, err := exec.LookPath(fields[0]); err == nil {
			switch strings.ToLower(filepath.Ext(p)) {
			case ".cmd", ".bat":
				argv = append([]string{"cmd.exe", "/c", p}, fields[1:]...)
			}
		}
	}
	if argv == nil {
		argv = fields
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	if dir != "" {
		cmd.Dir = dir
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr capped
	stderr.limit = 8 << 10
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := New(stdout, stdin, func() { _ = stdin.Close() })
	c.cmd = cmd
	c.stderr = &stderr
	return c, nil
}

// stderr collects the server's error output for death reports (capped).
type capped struct {
	mu    sync.Mutex
	buf   []byte
	limit int
}

func (c *capped) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.buf) < c.limit {
		room := c.limit - len(c.buf)
		if len(p) > room {
			p = p[:room]
		}
		c.buf = append(c.buf, p...)
	}
	return len(p), nil
}

func (c *capped) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.TrimSpace(string(c.buf))
}

// Alive reports whether the session is still up.
func (c *Client) Alive() bool { return c.alive.Load() }

// Err reports why the session ended, or nil while it runs.
func (c *Client) Err() error {
	c.pmu.Lock()
	defer c.pmu.Unlock()
	return c.dead
}

// Close ends the session: the read loop marks the client dead when the pipe
// closes, and a spawned process is killed outright.
func (c *Client) Close() error {
	if c.kick != nil {
		c.kick()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		return c.cmd.Process.Kill()
	}
	return nil
}

// Initialize performs the handshake and the required initialized
// notification, remembering what the server said about itself.
func (c *Client) Initialize(ctx context.Context, name, version string) (ServerInfo, error) {
	res, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": name, "version": version},
	})
	if err != nil {
		return ServerInfo{}, err
	}
	var out struct {
		ServerInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return ServerInfo{}, fmt.Errorf("mcp: bad initialize result: %w", err)
	}
	var caps []string
	for k := range out.Capabilities {
		caps = append(caps, k)
	}
	info := ServerInfo{
		Name:       out.ServerInfo.Name,
		Version:    out.ServerInfo.Version,
		Protocol:   out.ProtocolVersion,
		Capability: caps,
	}
	if err := c.notify(ctx, "notifications/initialized", nil); err != nil {
		return info, err
	}
	return info, nil
}

// ListTools returns every tool the server exposes, following cursor
// pagination to its end.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	var all []Tool
	cursor := ""
	for page := 0; page < 100; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		res, err := c.call(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var out struct {
			Tools      []Tool `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(res, &out); err != nil {
			return nil, fmt.Errorf("mcp: bad tools/list result: %w", err)
		}
		all = append(all, out.Tools...)
		if out.NextCursor == "" {
			break
		}
		cursor = out.NextCursor
	}
	return all, nil
}

// CallTool runs one tool with the given arguments object.
func (c *Client) CallTool(ctx context.Context, tool string, args map[string]any) (ToolOutcome, error) {
	if args == nil {
		args = map[string]any{}
	}
	res, err := c.call(ctx, "tools/call", map[string]any{"name": tool, "arguments": args})
	if err != nil {
		return ToolOutcome{}, err
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return ToolOutcome{}, fmt.Errorf("mcp: bad tools/call result: %w", err)
	}
	var b strings.Builder
	for _, item := range out.Content {
		switch item.Type {
		case "text", "":
			b.WriteString(item.Text)
		default:
			fmt.Fprintf(&b, "[non-text content: %s]", item.Type)
		}
	}
	return ToolOutcome{Text: b.String(), IsError: out.IsError}, nil
}

// call sends one request and waits for its response, or for ctx, or for the
// session to die.
func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if !c.Alive() {
		return nil, c.deathErr()
	}
	id := atomic.AddInt64(&c.next, 1)
	ch := make(chan frame, 1)
	c.pmu.Lock()
	if c.dead != nil {
		c.pmu.Unlock()
		return nil, c.deathErr()
	}
	c.pend[id] = ch
	c.pmu.Unlock()

	raw, _ := json.Marshal(params)
	f := frame{JSONRPC: "2.0", ID: json.RawMessage(fmt.Sprint(id)), Method: method, Params: raw}
	if err := c.write(f); err != nil {
		c.forget(id)
		return nil, err
	}
	select {
	case <-ctx.Done():
		c.forget(id)
		return nil, ctx.Err()
	case resp, ok := <-ch:
		if !ok { // the session died while the request was in flight
			return nil, c.deathErr()
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

// notify sends a one-way message.
func (c *Client) notify(ctx context.Context, method string, params any) error {
	_ = ctx
	if !c.Alive() {
		return c.deathErr()
	}
	f := frame{JSONRPC: "2.0", Method: method}
	if params != nil {
		f.Params, _ = json.Marshal(params)
	}
	return c.write(f)
}

func (c *Client) forget(id int64) {
	c.pmu.Lock()
	delete(c.pend, id)
	c.pmu.Unlock()
}

func (c *Client) deathErr() error {
	err := c.Err()
	if err == nil {
		err = ErrDead
	}
	var tail string
	if c.stderr != nil {
		tail = c.stderr.String()
	}
	if tail != "" {
		return fmt.Errorf("%w: %s", err, tail)
	}
	return err
}

func (c *Client) write(f frame) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.w.Write(b)
	return err
}

// readLoop dispatches inbound lines: responses wake their pending request,
// server requests are refused (the client advertised no capabilities), and
// notifications (logging and the like) are dropped.
func (c *Client) readLoop() {
	scan := bufio.NewScanner(c.in)
	scan.Buffer(make([]byte, 64<<10), maxLineLen)
	for scan.Scan() {
		var f frame
		if err := json.Unmarshal(scan.Bytes(), &f); err != nil {
			continue
		}
		switch {
		case f.Method == "" && f.ID != nil:
			c.deliver(f)
		case f.Method != "" && f.ID != nil:
			// A server-to-client request: answer method-not-found so the
			// server does not wait forever for a client that cannot help.
			_ = c.write(frame{JSONRPC: "2.0", ID: f.ID,
				Error: &rpcError{Code: -32601, Message: "not found"}})
		}
		// notifications (method without id) carry no answer
	}
	c.kill(c.deathReason(scan.Err()))
}

func (c *Client) deathReason(scanErr error) error {
	if scanErr != nil {
		return fmt.Errorf("%w: %v", ErrDead, scanErr)
	}
	return ErrDead
}

func (c *Client) deliver(f frame) {
	var id int64
	if _, err := fmt.Sscan(string(f.ID), &id); err != nil {
		return // an id the client never sent
	}
	c.pmu.Lock()
	ch, ok := c.pend[id]
	if ok {
		delete(c.pend, id)
	}
	c.pmu.Unlock()
	if ok {
		ch <- f
		close(ch)
	}
}

// kill marks the session dead and wakes every waiting caller.
func (c *Client) kill(err error) {
	c.pmu.Lock()
	if c.dead != nil {
		c.pmu.Unlock()
		return
	}
	c.dead = err
	pend := c.pend
	c.pend = map[int64]chan frame{}
	c.pmu.Unlock()
	c.alive.Store(false)
	for _, ch := range pend {
		close(ch)
	}
}
