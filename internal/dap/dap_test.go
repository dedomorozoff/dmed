package dap

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockServer emulates the adapter side over net.Pipe so the tests exercise the
// real Content-Length framing, request/response correlation and event dispatch
// without spawning a subprocess.
type mockServer struct {
	t    *testing.T
	srv  io.ReadWriteCloser
	read *bufio.Reader
	mu   sync.Mutex
	reqs chan mockReq
}

type mockReq struct {
	seq     int64
	command string
	body    json.RawMessage
}

type mockPair struct {
	client *Client
	server *mockServer
}

func newMockPair(t *testing.T) *mockPair {
	t.Helper()
	client, serverConn := net.Pipe()
	srv := &mockServer{
		t:    t,
		srv:  serverConn,
		read: bufio.NewReader(serverConn),
		reqs: make(chan mockReq, 32),
	}
	go srv.loop()
	cl := NewClient(client, func(Event) {})
	return &mockPair{client: cl, server: srv}
}

func (s *mockServer) loop() {
	for {
		contentLength := 0
		for {
			line, err := s.read.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if strings.HasPrefix(strings.ToLower(line), "content-length:") {
				var n int
				_, _ = fmt.Sscanf(line, "Content-Length: %d", &n)
				contentLength = n
			}
		}
		if contentLength <= 0 {
			continue
		}
		body := make([]byte, contentLength)
		if _, err := io.ReadFull(s.read, body); err != nil {
			return
		}
		var msg rpcResponse
		if err := json.Unmarshal(body, &msg); err != nil {
			continue
		}
		s.reqs <- mockReq{seq: msg.Seq, command: msg.Command, body: msg.Arguments}
	}
}

func (s *mockServer) respond(seq int64, body interface{}) {
	s.write(rpcResponse{
		Type: "response", Seq: seq + 1000, RequestSeq: seq, Success: true,
		Body: mustJSON(body),
	})
}

func (s *mockServer) respondError(seq int64, message string) {
	s.write(rpcResponse{Type: "response", Seq: seq + 1000, RequestSeq: seq, Success: false, Message: message})
}

func (s *mockServer) sendEvent(event string, body interface{}) {
	s.write(rpcResponse{Type: "event", Seq: newSeq(), Event: event, Body: mustJSON(body)})
}

func (s *mockServer) write(msg rpcResponse) {
	payload := mustJSON(msg)
	out := append(fmt.Appendf(nil, "Content-Length: %d\r\n\r\n", len(payload)), payload...)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.srv.Write(out); err != nil {
		s.t.Errorf("mock write: %v", err)
	}
}

func (s *mockServer) nextReq() mockReq {
	select {
	case r := <-s.reqs:
		return r
	case <-time.After(2 * time.Second):
		s.t.Error("timed out waiting for client request")
		return mockReq{}
	}
}

func mustJSON(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

var seqCounter int64

func init() {
	seqCounter = time.Now().UnixNano() & 0xffffff
}

func newSeq() int64 {
	seqCounter++
	return seqCounter
}

// ── Request/response plumbing ─────────────────────────────────────────────

func TestCallWireShapeAndCorrelation(t *testing.T) {
	p := newMockPair(t)
	defer p.client.Close()

	done := make(chan json.RawMessage, 1)
	go func() {
		body, _ := p.client.call("threads", map[string]interface{}{})
		done <- body
	}()

	req := p.server.nextReq()
	if req.command != "threads" {
		t.Fatalf("command = %q, want threads", req.command)
	}
	p.server.respond(req.seq, map[string]interface{}{"threads": []map[string]interface{}{}})

	select {
	case body := <-done:
		if body == nil {
			t.Fatal("got nil body for successful response")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("call did not return")
	}
}

func TestInitializeReadsCapabilities(t *testing.T) {
	p := newMockPair(t)
	defer p.client.Close()

	got := make(chan bool, 1)
	go func() {
		supports, _ := p.client.Initialize()
		got <- supports
	}()

	req := p.server.nextReq()
	if req.command != "initialize" {
		t.Fatalf("command = %q, want initialize", req.command)
	}
	p.server.respond(req.seq, map[string]interface{}{"supportsConfigurationDoneRequest": true})

	select {
	case supports := <-got:
		if !supports {
			t.Fatal("supportsConfigurationDoneRequest = false, want true")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Initialize did not return")
	}
}

func TestCallReturnsServerError(t *testing.T) {
	p := newMockPair(t)
	defer p.client.Close()

	got := make(chan error, 1)
	go func() {
		got <- p.client.Continue(1)
	}()

	req := p.server.nextReq()
	if req.command != "continue" {
		t.Fatalf("command = %q, want continue", req.command)
	}
	p.server.respondError(req.seq, "no such thread")

	select {
	case err := <-got:
		if err == nil || !strings.Contains(err.Error(), "no such thread") {
			t.Fatalf("err = %v, want failure mentioning server message", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Continue did not return")
	}
}

// ── Connect transport ─────────────────────────────────────────────────────

func TestStartConnectDialAndHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	addr := ln.Addr().String()

	type started struct {
		client *Client
		err    error
	}
	st := make(chan started, 1)
	go func() {
		cl, err := StartConnect(addr, "", func(Event) {})
		st <- started{client: cl, err: err}
	}()

	conn, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	srv := &mockServer{t: t, srv: conn, read: bufio.NewReader(conn), reqs: make(chan mockReq, 8)}
	go srv.loop()

	s := <-st
	if s.err != nil {
		t.Fatalf("StartConnect: %v", s.err)
	}
	cl := s.client
	defer cl.Close()

	supports := make(chan bool, 1)
	go func() {
		s, _ := cl.Initialize()
		supports <- s
	}()
	req := srv.nextReq()
	if req.command != "initialize" {
		t.Fatalf("command = %q, want initialize", req.command)
	}
	srv.respond(req.seq, map[string]interface{}{"supportsConfigurationDoneRequest": true})
	select {
	case s := <-supports:
		if !s {
			t.Fatal("supportsConfigurationDoneRequest = false, want true")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Initialize over connect transport did not return")
	}
}

func TestStartConnectEmptyAddr(t *testing.T) {
	if _, err := StartConnect("", "", func(Event) {}); err == nil {
		t.Fatal("want error for empty address")
	}
}

func TestStartConnectRefused(t *testing.T) {
	// Grab an ephemeral port and release it: nothing listens there, so the
	// dial must fail fast instead of hanging.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	_, err = StartConnect(addr, "", func(Event) {})
	if err == nil {
		t.Fatal("want error for closed endpoint")
	}
	if !strings.Contains(err.Error(), "dap connect") {
		t.Fatalf("err = %v, want dap connect error", err)
	}
}

// ── Event dispatch ────────────────────────────────────────────────────────

func TestEventsDispatch(t *testing.T) {
	client, serverConn := net.Pipe()
	defer serverConn.Close()
	got := make(chan Event, 16)
	cl := NewClient(client, func(e Event) { got <- e })
	defer cl.Close()

	srv := &mockServer{t: t, srv: serverConn, read: bufio.NewReader(serverConn), reqs: make(chan mockReq, 8)}
	go srv.loop() // consume client writes (e.g. the disconnect from Close)

	srv.sendEvent("stopped", map[string]interface{}{"reason": "breakpoint", "threadId": 7})
	srv.sendEvent("output", map[string]interface{}{"category": "stdout", "output": "hello\n"})
	srv.sendEvent("terminated", map[string]interface{}{})

	want := []EventKind{EventStopped, EventOutput, EventTerminated}
	for i, wantKind := range want {
		select {
		case e := <-got:
			if e.Kind != wantKind {
				t.Fatalf("event %d kind = %v, want %v", i, e.Kind, wantKind)
			}
			if i == 1 && e.Output != "hello\n" {
				t.Fatalf("output event = %q", e.Output)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("event %d never arrived", i)
		}
	}
}

// ── Result decoding ───────────────────────────────────────────────────────

func TestStackTraceScopesVariablesDecode(t *testing.T) {
	p := newMockPair(t)
	defer p.client.Close()

	go func() {
		_, _ = p.client.StackTrace(3, 50)
	}()
	req := p.server.nextReq()
	if req.command != "stackTrace" {
		t.Fatalf("command = %q, want stackTrace", req.command)
	}
	p.server.respond(req.seq, map[string]interface{}{
		"stackFrames": []map[string]interface{}{
			{"id": 11, "name": "main.main", "line": 14, "column": 8,
				"source": map[string]interface{}{"path": "/tmp/x.go", "name": "x.go"}},
		},
	})

	go func() {
		_, _ = p.client.Scopes(11)
	}()
	req2 := p.server.nextReq()
	if req2.command != "scopes" {
		t.Fatalf("command = %q, want scopes", req2.command)
	}
	p.server.respond(req2.seq, map[string]interface{}{
		"scopes": []map[string]interface{}{
			{"name": "Locals", "variablesReference": 55, "expensive": true},
		},
	})

	go func() {
		_, _ = p.client.Variables(55)
	}()
	req3 := p.server.nextReq()
	if req3.command != "variables" {
		t.Fatalf("command = %q, want variables", req3.command)
	}
	p.server.respond(req3.seq, map[string]interface{}{
		"variables": []map[string]interface{}{
			{"name": "n", "value": "42", "type": "int", "variablesReference": 0},
		},
	})
}

func TestEvaluate(t *testing.T) {
	p := newMockPair(t)
	defer p.client.Close()

	done := make(chan Variable, 1)
	go func() {
		v, _ := p.client.Evaluate("2 + 2", 11)
		done <- v
	}()

	req := p.server.nextReq()
	if req.command != "evaluate" {
		t.Fatalf("command = %q, want evaluate", req.command)
	}
	p.server.respond(req.seq, map[string]interface{}{"result": "4", "variablesReference": 0})

	select {
	case v := <-done:
		if v.Value != "4" || v.Name != "2 + 2" {
			t.Fatalf("diff = %+v", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Evaluate did not return")
	}
}

func TestBreakpointResultVerified(t *testing.T) {
	p := newMockPair(t)
	defer p.client.Close()

	done := make(chan []Breakpoint, 1)
	go func() {
		bps, _ := p.client.SetBreakpoints("/tmp/x.go", []int{3})
		done <- bps
	}()

	req := p.server.nextReq()
	if req.command != "setBreakpoints" {
		t.Fatalf("command = %q, want setBreakpoints", req.command)
	}
	var args struct {
		Source      map[string]string `json:"source"`
		Breakpoints []map[string]int  `json:"breakpoints"`
	}
	if err := json.Unmarshal(req.body, &args); err != nil {
		t.Fatalf("decode request args: %v", err)
	}
	if args.Source["path"] != "/tmp/x.go" || len(args.Breakpoints) != 1 || args.Breakpoints[0]["line"] != 3 {
		t.Fatalf("args = %+v", args)
	}
	p.server.respond(req.seq, map[string]interface{}{
		"breakpoints": []map[string]interface{}{
			{"verified": true, "line": 3},
		},
	})

	select {
	case bps := <-done:
		if len(bps) != 1 || !bps[0].Verified {
			t.Fatalf("bpm = %+v", bps)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SetBreakpoints did not return")
	}
}
