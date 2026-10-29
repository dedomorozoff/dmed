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

// TestParseFormatEdits pins the shape of a textDocument/formatting reply: a
// bare TextEdit array (unlike the "changes"/"documentChanges" maps rename and
// code actions return), and an empty/null answer meaning "nothing to do".
func TestParseFormatEdits(t *testing.T) {
	abs := "/home/u/main.go"
	if uriToPath(pathToURI(abs)) == "" {
		t.Fatal("sanity: uri round-trip must resolve")
	}
	res := json.RawMessage(`[
		{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},
		 "newText":"package"},
		{"range":{"start":{"line":1,"character":2},"end":{"line":1,"character":3}},
		 "newText":"  x"}
	]`)
	ed := parseFormatEdits(res, abs)
	if ed == nil || len(ed.Files) != 1 {
		t.Fatalf("want one formatted file, got %+v", ed)
	}
	for p, edits := range ed.Files {
		ur := uriToPath(pathToURI(abs))
		if p != ur {
			t.Fatalf("file key = %q, want the URI-normalized path %q", p, ur)
		}
		if len(edits) != 2 || edits[0].NewText != "package" || edits[1].EndCol != 3 {
			t.Fatalf("edits = %+v", edits)
		}
	}
	if parseFormatEdits(json.RawMessage("null"), abs) != nil {
		t.Fatal("a null reply must mean no edits")
	}
	if parseFormatEdits(json.RawMessage("[]"), abs) != nil {
		t.Fatal("an empty reply must mean no edits")
	}
	if parseFormatEdits(json.RawMessage("{broken"), abs) != nil {
		t.Fatal("a malformed reply must mean no edits, not a crash")
	}
}

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

	p, _ := filepath.Abs("main.go") // host-native, so the URI is cwd-independent on every OS
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
	if !strings.Contains(r.buf.String(), "didOpen") || !strings.Contains(r.buf.String(), strings.ReplaceAll(p, "\\", "/")) {
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

func TestColToRune(t *testing.T) {
	ascii := []rune("abc")
	astral := []rune("😀ab") // 😀 is 2 UTF-16 units
	cases := []struct {
		name string
		line []rune
		col  int
		want int
	}{
		{"negative", ascii, -3, 0},
		{"zero", ascii, 0, 0},
		{"mid", ascii, 1, 1},
		{"at len", ascii, 3, 3},
		{"past end clamps", ascii, 99, 3},
		{"after astral", astral, 2, 1},
		{"inside astral follower", astral, 3, 2},
		{"past astral word", astral, 4, 3},
		{"past astral line clamps", astral, 99, 3},
		{"astral alone len", []rune("😀"), 2, 1},
	}
	for _, tc := range cases {
		if got := ColToRune(tc.line, tc.col); got != tc.want {
			t.Errorf("ColToRune(%q, %d) [%s] = %d, want %d", string(tc.line), tc.col, tc.name, got, tc.want)
		}
	}
}

func TestSortedEdits(t *testing.T) {
	in := []TextEdit{
		{StartLine: 0, StartCol: 0},
		{StartLine: 2, StartCol: 5},
		{StartLine: 0, StartCol: 9},
		{StartLine: 1, StartCol: 0},
	}
	out := SortedEdits(in)
	want := []TextEdit{
		{StartLine: 2, StartCol: 5},
		{StartLine: 1, StartCol: 0},
		{StartLine: 0, StartCol: 9},
		{StartLine: 0, StartCol: 0},
	}
	if len(out) != len(want) {
		t.Fatalf("len = %d, want %d", len(out), len(want))
	}
	for i := range want {
		if out[i] != want[i] {
			t.Errorf("out[%d] = %+v, want %+v", i, out[i], want[i])
		}
	}
	// The input must stay untouched.
	if in[0].StartLine != 0 || in[1].StartLine != 2 {
		t.Fatalf("SortedEdits mutated its input: %+v", in)
	}
}

func TestApplyEditsToText(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		edits []TextEdit
		want  string
	}{
		{
			"single-line replace",
			"hello world\nsecond\n",
			[]TextEdit{{StartLine: 0, StartCol: 6, EndLine: 0, EndCol: 11, NewText: "there"}},
			"hello there\nsecond\n",
		},
		{
			"whole-line delete via empty replacement",
			"keep\ndrop\nkeep2\n",
			[]TextEdit{{StartLine: 1, StartCol: 0, EndLine: 1, EndCol: 4, NewText: ""}},
			"keep\n\nkeep2\n",
		},
		{
			"range spanning two lines",
			"abc def\nnext line\n",
			[]TextEdit{{StartLine: 0, StartCol: 4, EndLine: 1, EndCol: 4, NewText: "gone"}},
			"abc gone line\n",
		},
		{
			"multi-line insertion",
			"abcd\nefgh\n",
			[]TextEdit{{StartLine: 0, StartCol: 4, EndLine: 0, EndCol: 4, NewText: "X\nY"}},
			"abcdX\nY\nefgh\n",
		},
		{
			"two edits right-to-left",
			"a b c\n",
			[]TextEdit{
				{StartLine: 0, StartCol: 4, EndLine: 0, EndCol: 5, NewText: "Z"},
				{StartLine: 0, StartCol: 0, EndLine: 0, EndCol: 1, NewText: "W"},
			},
			"W b Z\n",
		},
		{
			"out-of-range skipped",
			"one\n",
			[]TextEdit{{StartLine: 5, StartCol: 0, EndLine: 5, EndCol: 3, NewText: "x"}},
			"one\n",
		},
	}
	for _, tc := range cases {
		if got := ApplyEditsToText(tc.text, tc.edits); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

func TestParseWorkspaceEdit(t *testing.T) {
	p := uriToPath("file:///proj/f.go")
	res := json.RawMessage(`{"changes": {"file:///proj/f.go": [
		{"range": {"start": {"line": 1, "character": 2}, "end": {"line": 1, "character": 5}}, "newText": "x"}
	]}}`)
	we := parseWorkspaceEdit(res)
	if we == nil || len(we.Files[p]) != 1 {
		t.Fatalf("changes shape: %+v", we)
	}
	e := we.Files[p][0]
	if e.StartLine != 1 || e.StartCol != 2 || e.EndLine != 1 || e.EndCol != 5 || e.NewText != "x" {
		t.Fatalf("changes edit: %+v", e)
	}

	res = json.RawMessage(`{"documentChanges": [{"textDocument": {"uri": "file:///proj/f.go"}, "edits": [
		{"range": {"start": {"line": 0, "character": 1}, "end": {"line": 0, "character": 1}}, "newText": "y"}
	]}]}`)
	we = parseWorkspaceEdit(res)
	if we == nil || len(we.Files[p]) != 1 || we.Files[p][0].NewText != "y" {
		t.Fatalf("documentChanges shape: %+v", we)
	}

	// Nothing found → nil, not an empty edit set.
	if parseWorkspaceEdit(json.RawMessage(`{}`)) != nil {
		t.Fatal("empty reply must parse to nil")
	}
	if parseWorkspaceEdit(json.RawMessage(`null`)) != nil {
		t.Fatal("null reply must parse to nil")
	}
}
