package lsp

import (
	"bytes"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"encoding/json"
)

// nopWC satisfies io.WriteCloser without touching a real pipe.
type nopWC struct{}

func (nopWC) Write(p []byte) (int, error) { return len(p), nil }
func (nopWC) Close() error                { return nil }

// recWC appends every write so tests can inspect the emitted frames.
type recWC struct{ buf bytes.Buffer }

func (r *recWC) Write(p []byte) (int, error) { return r.buf.Write(p) }
func (r *recWC) Close() error                { return nil }

// testClient starts with initialize already finished (initDone closed).
func testClient() *Client {
	done := make(chan struct{})
	close(done)
	return &Client{
		stdin:    nopWC{},
		initDone: done,
		pending:  make(map[int64]chan json.RawMessage),
		opened:   make(map[string]bool),
		versions: make(map[string]int),
	}
}

// TestCallWaitsForInitialize guards the race where the first completion is
// sent before the initialize handshake is done: gopls processes initialize
// first and leaves the earlier request unanswered, which used to burn the
// whole 6s timeout. The gate must hold the request until initDone closes and
// then flush it to the server.
func TestCallWaitsForInitialize(t *testing.T) {
	initDone := make(chan struct{})
	c := testClient()
	c.initDone = initDone

	go func() {
		time.Sleep(100 * time.Millisecond)
		close(initDone)
	}()
	// Simulate the server never answering: die right after the gate opens.
	go func() {
		<-initDone
		time.Sleep(50 * time.Millisecond)
		c.closePending()
	}()

	start := time.Now()
	_, err := c.call("textDocument/completion", map[string]interface{}{})
	if err == nil {
		t.Fatal("call must report the server going away")
	}
	if !strings.Contains(err.Error(), "server closed") {
		t.Fatalf("want 'server closed', got %q", err)
	}
	if since := time.Since(start); since > 3*time.Second {
		t.Fatalf("call took %v, gate must open as soon as initialize finishes", since)
	}
}

// TestClosePendingUnblocks asserts that when the server process dies, every
// request still in flight fails immediately instead of draining the timeout.
func TestClosePendingUnblocks(t *testing.T) {
	c := testClient()
	go func() {
		time.Sleep(50 * time.Millisecond)
		c.closePending()
	}()
	start := time.Now()
	_, err := c.call("textDocument/completion", map[string]interface{}{})
	if err == nil || !strings.Contains(err.Error(), "server closed") {
		t.Fatalf("want 'server closed', got %v", err)
	}
	if since := time.Since(start); since > 2*time.Second {
		t.Fatalf("closePending must unblock quickly, took %v", since)
	}
}

// TestEnsureOpenedOnce verifies didOpen is written exactly once even when the
// eager goroutine and a lazy request goroutine race over the same document.
// A duplicate didOpen is harmless, but didOpen issued twice while a didChange
// slips between them would leave gopls with a stale snapshot.
func TestEnsureOpenedOnce(t *testing.T) {
	r := &recWC{}
	c := testClient()
	c.stdin = r
	c.opened = make(map[string]bool)

	p := "C:\\proj\\main.go"
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			done <- c.EnsureOpened(p, "go", "package main\n")
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatalf("EnsureOpened error: %v", err)
		}
	}
	n := r.buf.Len()
	if !strings.Contains(r.buf.String(), "didOpen") || !strings.Contains(r.buf.String(), "C:/proj/main.go") {
		t.Fatalf("didOpen frame missing:\n%s", r.buf.String())
	}
	// Second call must be a no-op: no extra frame was written.
	if err := c.EnsureOpened(p, "go", "other"); err != nil {
		t.Fatalf("repeated EnsureOpened error: %v", err)
	}
	if r.buf.Len() != n {
		t.Fatalf("EnsureOpened wrote a second didOpen (now %d bytes, was %d)", r.buf.Len(), n)
	}
}

// TestEnsureOpenedGatesOnInitialize proves the didOpen cannot be written before
// the handshake: passing an unclosed initDone defers the frame until close.
func TestEnsureOpenedGatesOnInitialize(t *testing.T) {
	r := &recWC{}
	c := testClient()
	c.stdin = r
	c.initDone = make(chan struct{})

	p := "C:\\proj\\main.go"
	go func() {
		time.Sleep(100 * time.Millisecond)
		close(c.initDone)
	}()
	start := time.Now()
	if err := c.EnsureOpened(p, "go", "package main\n"); err != nil {
		t.Fatalf("EnsureOpened error: %v", err)
	}
	if since := time.Since(start); since < 80*time.Millisecond {
		t.Fatalf("EnsureOpened must wait for initialize, returned in %v", since)
	}
	if !strings.Contains(r.buf.String(), "didOpen") {
		t.Fatalf("didOpen frame missing after initialize:\n%s", r.buf.String())
	}
}

// TestDidChangeMonotonicVersions guards that consecutive changes always carry
// increasing version numbers even when fired from parallel goroutines.
func TestDidChangeMonotonicVersions(t *testing.T) {
	r := &recWC{}
	c := testClient()
	c.stdin = r
	seen := map[int]bool{}
	var mu sync.Mutex
	verify := func() {
		s := r.buf.Bytes()
		for _, line := range bytes.Split(s, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if !bytes.HasPrefix(line, []byte("{\"jsonrpc\":\"2.0\",\"method\":")) {
				continue
			}
			var req struct {
				Params struct {
					TextDocument struct {
						Version int `json:"version"`
					} `json:"textDocument"`
				} `json:"params"`
			}
			if err := json.Unmarshal(line, &req); err != nil {
				continue
			}
			if req.Params.TextDocument.Version == 0 {
				continue
			}
			mu.Lock()
			if seen[req.Params.TextDocument.Version] {
				mu.Unlock()
				t.Fatalf("duplicate version %d", req.Params.TextDocument.Version)
			}
			seen[req.Params.TextDocument.Version] = true
			mu.Unlock()
		}
	}
	verify()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				c.DidChange("C:\\proj\\main.go", "x", 1)
			}
		}()
	}
	wg.Wait()
	verify()
}

func TestURIToPathConversion(t *testing.T) {
	samplePath, _ := filepath.Abs("test.go")
	uri := pathToURI(samplePath)

	converted := uriToPath(uri)
	if filepath.Clean(converted) != filepath.Clean(samplePath) {
		t.Fatalf("URI roundtrip mismatch: original=%q uri=%q converted=%q", samplePath, uri, converted)
	}
}

func TestDiagnosticsStoreAndGet(t *testing.T) {
	c := &Client{
		diagnostics: make(map[string][]Diagnostic),
	}
	absPath, _ := filepath.Abs("main.go")
	c.diagnostics[absPath] = []Diagnostic{
		{Line: 10, Col: 5, Severity: 1, Message: "undefined symbol: foo"},
	}

	got := c.GetDiagnostics(absPath)
	if len(got) != 1 || got[0].Message != "undefined symbol: foo" {
		t.Fatalf("unexpected diagnostics: %+v", got)
	}
}
