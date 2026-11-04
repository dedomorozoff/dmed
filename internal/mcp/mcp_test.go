package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

// The tests drive the real protocol over net.Pipe: one JSON-RPC frame per
// line, the exact stdio transport minus the process. The fake server answers
// by method inline in each test.

// pair returns a live client and the server end of the pipe. net.Pipe is a
// duplex pair of two endpoints: the client lives on one endpoint entirely
// (it reads and writes there), and everything the test does as the server
// lives on the other. Putting the client's reader on the far end would make
// it read back its own requests.
func pair(t *testing.T) (*Client, net.Conn) {
	t.Helper()
	a, b := net.Pipe()
	c := New(a, a, nil)
	t.Cleanup(func() {
		_ = c.Close()
		a.Close()
		b.Close()
	})
	return c, b
}

// serve answers every request on conn through h until the pipe closes.
// h returns the result payload or an error to report.
func serve(conn net.Conn, h func(method string, params json.RawMessage) (any, error)) {
	go func() {
		scan := bufio.NewScanner(conn)
		scan.Buffer(make([]byte, 64<<10), 1<<20)
		for scan.Scan() {
			var f frame
			if err := json.Unmarshal(scan.Bytes(), &f); err != nil || f.ID == nil || f.Method == "" {
				continue
			}
			resp := frame{JSONRPC: "2.0", ID: f.ID}
			if res, err := h(f.Method, f.Params); err != nil {
				resp.Error = &rpcError{Code: -32000, Message: err.Error()}
			} else {
				resp.Result, _ = json.Marshal(res)
			}
			b, _ := json.Marshal(resp)
			conn.Write(append(b, '\n'))
		}
	}()
}

func TestFullSession(t *testing.T) {
	c, srv := pair(t)
	serve(srv, func(method string, params json.RawMessage) (any, error) {
		switch method {
		case "initialize":
			var p map[string]any
			json.Unmarshal(params, &p)
			if p["protocolVersion"] != ProtocolVersion {
				return nil, &rpcError{Code: -32602, Message: "unsupported protocol"}
			}
			return map[string]any{
				"protocolVersion": ProtocolVersion,
				"serverInfo":      map[string]any{"name": "test-server", "version": "9"},
				"capabilities":    map[string]any{"tools": map[string]any{}},
			}, nil
		case "tools/list":
			var p map[string]any
			json.Unmarshal(params, &p)
			if p["cursor"] == nil {
				return map[string]any{
					"tools":      []Tool{{Name: "alpha", Description: "first"}},
					"nextCursor": "page2",
				}, nil
			}
			return map[string]any{
				"tools": []Tool{{Name: "beta", InputSchema: json.RawMessage(`{"type":"object"}`)}},
			}, nil
		case "tools/call":
			return map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": "hi "},
					{"type": "text", "text": "there"},
					{"type": "image", "mimeType": "image/png"},
				},
			}, nil
		}
		return nil, &rpcError{Code: -32601, Message: "not found"}
	})

	ctx := context.Background()
	info, err := c.Initialize(ctx, "dmed", "test")
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if info.Name != "test-server" || info.Version != "9" {
		t.Fatalf("info = %+v", info)
	}
	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tools) != 2 || tools[0].Name != "alpha" || tools[1].Name != "beta" {
		t.Fatalf("pagination lost: %+v", tools)
	}
	if len(tools[1].InputSchema) == 0 {
		t.Fatal("inputSchema lost")
	}
	out, err := c.CallTool(ctx, "alpha", map[string]any{"q": "x"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if out.Text != "hi there[non-text content: image]" || out.IsError {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestToolErrorFlagged(t *testing.T) {
	c, srv := pair(t)
	serve(srv, func(method string, params json.RawMessage) (any, error) {
		if method == "tools/call" {
			return map[string]any{
				"content": []map[string]any{{"type": "text", "text": "cannot do"}},
				"isError": true,
			}, nil
		}
		return map[string]any{}, nil
	})
	ctx := context.Background()
	if _, err := c.Initialize(ctx, "dmed", "t"); err != nil {
		t.Fatal(err)
	}
	out, err := c.CallTool(ctx, "x", nil)
	if err != nil {
		t.Fatalf("a tool-level error is a response, not a protocol error: %v", err)
	}
	if !out.IsError || out.Text != "cannot do" {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestJSONRPCErrorPropagates(t *testing.T) {
	c, srv := pair(t)
	serve(srv, func(method string, params json.RawMessage) (any, error) {
		return nil, &rpcError{Code: -32000, Message: "boom"}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := c.Initialize(ctx, "dmed", "t")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want the server message", err)
	}
}

func TestRefusesServerRequests(t *testing.T) {
	c, srv := pair(t)
	// net.Pipe is unbuffered: a write blocks until a read takes it, so the
	// server end needs a reader running while it also writes.
	got := make(chan string, 4)
	go func() {
		scan := bufio.NewScanner(srv)
		for scan.Scan() {
			got <- scan.Text()
		}
	}()
	// A server that asks the client something must get method-not-found back,
	// not silence: a sampling-capable server would otherwise wait forever.
	srv.Write([]byte(`{"jsonrpc":"2.0","id":77,"method":"sampling/createMessage","params":{}}` + "\n"))
	select {
	case line := <-got:
		var f frame
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			t.Fatalf("bad reply %q: %v", line, err)
		}
		if f.Error == nil || f.Error.Code != -32601 {
			t.Fatalf("reply = %+v, want a -32601 error", f)
		}
		if string(f.ID) != "77" {
			t.Fatalf("reply id = %s, want the request id echoed", f.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no refusal reply")
	}
	// A notification must be dropped without any answer at all.
	srv.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/message","params":{}}` + "\n"))
	// Give a wrong reply the chance to arrive before declaring silence.
	srv.Write([]byte(`{"jsonrpc":"2.0","id":78,"method":"ping"}` + "\n"))
	select {
	case line := <-got:
		var f frame
		json.Unmarshal([]byte(line), &f)
		if f.ID == nil || string(f.ID) != "78" {
			t.Fatalf("notification was answered: %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the follow-up request was not answered either")
	}
	_ = c
}

func TestDeathWakesPendingCall(t *testing.T) {
	a, b := net.Pipe()
	c := New(a, a, nil)
	// The server end reads the request and then dies without answering: the
	// call must return at once, not wait for its context deadline.
	go func() {
		buf := make([]byte, 512)
		b.Read(buf)
		a.Close()
		b.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	_, err := c.call(ctx, "initialize", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("err = %v, want a death error", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("pending callers must be woken the moment the stream ends")
	}
	if c.Alive() {
		t.Fatal("Alive must be false after the stream ended")
	}
}

func TestExposeAndManager(t *testing.T) {
	if got := ExposedName("file-system", "read file"); got != "mcp_file-system_read_file" {
		t.Fatalf("ExposedName = %q", got)
	}
	mgr := NewManager([]Result{
		{Name: "ok", Client: newDeadClientForTest(), Tools: []Tool{{Name: "t1"}, {Name: strings.Repeat("x", 70)}}},
		{Name: "bad", Err: "exit status 1"},
		{Name: "silent"},
	})
	if _, ok := mgr.Lookup("mcp_ok_t1"); !ok {
		t.Fatal("the good tool is missing")
	}
	if n := len(mgr.Failures()); n != 3 {
		t.Fatalf("failures = %v, want the bad server, the over-long name and the silent result", mgr.Failures())
	}
	if defs := mgr.Defs(); len(defs) != 1 || defs[0].Name != "mcp_ok_t1" {
		t.Fatalf("Defs = %+v", defs)
	}
	if _, err := mgr.Call(context.Background(), Exposed{Server: "ok", Tool: "t1"}, "{}"); err == nil {
		t.Fatal("calling a dead client must report it")
	}
	mgr.Close()
}

// newDeadClientForTest is a client whose stream is already closed; the
// manager keeps it as a running server but a call finds it dead.
func newDeadClientForTest() *Client {
	a, b := net.Pipe()
	c := New(a, a, nil)
	a.Close()
	b.Close()
	// let the read loop observe the death
	time.Sleep(20 * time.Millisecond)
	return c
}
