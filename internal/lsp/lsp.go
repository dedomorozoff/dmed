package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dmed/internal/debug"
)

// Diagnostic represents a compiler error, warning, or hint.
type Diagnostic struct {
	Line     int // 0-indexed
	Col      int // 0-indexed
	EndLine  int // 0-indexed
	EndCol   int // 0-indexed
	Severity int // 1: Error, 2: Warning, 3: Info, 4: Hint
	Message  string
	Source   string
}

// Location represents a target location in a file.
type Location struct {
	Path string
	Line int // 0-indexed
	Col  int // 0-indexed
}

// CompletionItem is a single LSP textDocument/completion candidate.
type CompletionItem struct {
	Label  string
	Kind   int
	Detail string
}

// Client is a JSON-RPC 2.0 LSP client over stdin/stdout.
type Client struct {
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdout      io.ReadCloser
	nextID      int64
	rootURI     string
	mu          sync.Mutex
	writeMu     sync.Mutex    // serializes ALL writes + the didOpen transition
	initDone    chan struct{} // closed once the initialize handshake finished
	pending     map[int64]chan json.RawMessage
	opened      map[string]bool
	versions    map[string]int
	diagnostics map[string][]Diagnostic
	diagMu      sync.RWMutex
	onDiag      func(path string, diags []Diagnostic)
}

type rpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      *int64      `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Start launches the language server command (e.g. "gopls") with project root.
func Start(serverCmd string, args []string, rootDir string, onDiag func(path string, diags []Diagnostic)) (*Client, error) {
	cmd := exec.Command(serverCmd, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}

	rootAbs, _ := filepath.Abs(rootDir)
	rootURI := pathToURI(rootAbs)

	c := &Client{
		cmd:         cmd,
		stdin:       stdin,
		stdout:      stdout,
		rootURI:     rootURI,
		initDone:    make(chan struct{}),
		pending:     make(map[int64]chan json.RawMessage),
		opened:      make(map[string]bool),
		versions:    make(map[string]int),
		diagnostics: make(map[string][]Diagnostic),
		onDiag:      onDiag,
	}

	go debug.CapturePanicReport(c.readLoop)
	go debug.CapturePanicReport(c.initialize)

	return c, nil
}

func pathToURI(path string) string {
	// Replace backslashes on every host: filepath.ToSlash only does so under
	// Windows, and drive-letter paths must map to file:///C:/... identically
	// regardless of where the editor or a test runs.
	path = strings.ReplaceAll(path, "\\", "/")
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return "file://" + (&url.URL{Path: path}).EscapedPath()
}

func uriToPath(uriStr string) string {
	u, err := url.Parse(uriStr)
	if err != nil {
		return uriStr
	}
	path := u.Path
	// On Windows, file:///c:/path -> c:/path
	if len(path) > 2 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return filepath.FromSlash(path)
}

func (c *Client) initialize() {
	params := map[string]interface{}{
		"processId": nil,
		"rootUri":   c.rootURI,
		"capabilities": map[string]interface{}{
			"textDocument": map[string]interface{}{
				"definition": map[string]interface{}{"dynamicRegistration": false},
				"hover":      map[string]interface{}{"dynamicRegistration": false},
				"formatting": map[string]interface{}{"dynamicRegistration": false},
				"completion": map[string]interface{}{"dynamicRegistration": false},
			},
		},
	}
	_, _ = c.call("initialize", params)
	_ = c.notify("initialized", map[string]interface{}{})
	// Only now may other messages (didOpen/didChange/completion) be written:
	// gopls accepts them only after the initialize handshake, and messages that
	// arrive beforehand make the first completion hang until it times out.
	close(c.initDone)
}

func (c *Client) Close() error {
	_ = c.notify("exit", nil)
	_ = c.stdin.Close()
	_ = c.stdout.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	return nil
}

func (c *Client) readLoop() {
	r := bufio.NewReader(c.stdout)
	for {
		// Read Content-Length header
		contentLength := 0
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				c.closePending()
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
			c.closePending()
			return
		}

		var resp rpcResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			continue
		}

		if resp.ID != nil {
			c.mu.Lock()
			ch, ok := c.pending[*resp.ID]
			delete(c.pending, *resp.ID)
			c.mu.Unlock()
			if ok {
				ch <- resp.Result
			}
		} else if resp.Method != "" {
			c.handleNotification(resp.Method, resp.Params)
		}
	}
}

func (c *Client) handleNotification(method string, params json.RawMessage) {
	if method == "textDocument/publishDiagnostics" {
		var diagParams struct {
			URI         string `json:"uri"`
			Diagnostics []struct {
				Range struct {
					Start struct {
						Line      int `json:"line"`
						Character int `json:"character"`
					} `json:"start"`
					End struct {
						Line      int `json:"line"`
						Character int `json:"character"`
					} `json:"end"`
				} `json:"range"`
				Severity int    `json:"severity"`
				Message  string `json:"message"`
				Source   string `json:"source"`
			} `json:"diagnostics"`
		}
		if err := json.Unmarshal(params, &diagParams); err == nil {
			path := uriToPath(diagParams.URI)
			var diags []Diagnostic
			for _, d := range diagParams.Diagnostics {
				diags = append(diags, Diagnostic{
					Line:     d.Range.Start.Line,
					Col:      d.Range.Start.Character,
					EndLine:  d.Range.End.Line,
					EndCol:   d.Range.End.Character,
					Severity: d.Severity,
					Message:  d.Message,
					Source:   d.Source,
				})
			}
			c.diagMu.Lock()
			c.diagnostics[path] = diags
			c.diagMu.Unlock()

			if c.onDiag != nil {
				c.onDiag(path, diags)
			}
		}
	}
}

func (c *Client) GetDiagnostics(path string) []Diagnostic {
	abs, _ := filepath.Abs(path)
	c.diagMu.RLock()
	defer c.diagMu.RUnlock()
	return c.diagnostics[abs]
}

// callTimeout bounds how long call blocks waiting for a server response. It
// must stay generous: on a cold start gopls indexes the whole module graph
// before it can answer the first completion, which can take close to a minute
// on a slow machine. Calls run off the UI thread, so a long cap costs nothing
// interactively and only turns a genuinely hung server into an error.
const callTimeout = 60 * time.Second

// closePending unblocks every call still waiting for a response. Called from
// the read loop once the server process dies, so callers error out promptly
// instead of draining the full timeout.
func (c *Client) closePending() {
	c.mu.Lock()
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
}

func (c *Client) call(method string, params interface{}) (json.RawMessage, error) {
	// All requests must wait for the initialize handshake: gopls processes
	// initialize first and will not answer completion/definition requests that
	// arrive before it. Without this gate the first completion races the init
	// goroutine and times out.
	if method != "initialize" {
		select {
		case <-c.initDone:
		case <-time.After(callTimeout):
			return nil, fmt.Errorf("lsp: %s timed out before initialize", method)
		}
	}
	id := atomic.AddInt64(&c.nextID, 1)
	ch := make(chan json.RawMessage, 1)

	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	c.writeMu.Lock()
	_, err = c.stdin.Write(append([]byte(header), data...))
	c.writeMu.Unlock()
	if err != nil {
		return nil, err
	}

	select {
	case res, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("lsp: %s server closed", method)
		}
		return res, nil
	case <-time.After(callTimeout):
		return nil, fmt.Errorf("lsp: %s timed out", method)
	}
}

func (c *Client) notify(method string, params interface{}) error {
	// No live transport: a stub client in tests (or a closed one). Drop the
	// notification instead of stalling every caller on the initialize
	// handshake of a dead session.
	if c.stdin == nil {
		return nil
	}
	// Notifications are also gated on the initialize handshake. A didOpen
	// written before initialize makes gopls never answer the subsequent
	// completion; DidChange may be issued by any goroutine at any time, so
	// every write path must wait, not just the request path.
	if method != "initialized" && method != "exit" {
		select {
		case <-c.initDone:
		case <-time.After(callTimeout):
			return fmt.Errorf("lsp: %s timed out before initialize", method)
		}
	}
	req := rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	c.writeMu.Lock()
	_, err = c.stdin.Write(append([]byte(header), data...))
	c.writeMu.Unlock()
	return err
}

// EnsureOpened sends textDocument/didOpen for path exactly once. The eager
// goroutine in the editor and the lazy request goroutines may race; holding
// writeMu across the "decide to open → written → marked" span guarantees a
// later didChange can never hit the pipe before didOpen, which gopls would
// silently drop and resurface as an empty completion.
func (c *Client) EnsureOpened(path, languageID, text string) error {
	// didOpen must never reach gopls before initialize: it would hang the
	// first completion. Wait for the handshake first, then serialize the write
	// against didChange via writeMu.
	if c.stdin == nil {
		return nil
	}
	select {
	case <-c.initDone:
	case <-time.After(callTimeout):
		return fmt.Errorf("lsp: didOpen timed out before initialize")
	}
	abs, _ := filepath.Abs(path)
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	if c.opened[abs] {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":        pathToURI(abs),
			"languageId": languageID,
			"version":    1,
			"text":       text,
		},
	}
	req := rpcRequest{
		JSONRPC: "2.0",
		Method:  "textDocument/didOpen",
		Params:  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	_, err = c.stdin.Write(append([]byte(header), data...))
	if err == nil {
		c.mu.Lock()
		c.opened[abs] = true
		c.mu.Unlock()
	}
	return err
}

// DidOpen informs the server that a document was opened.
func (c *Client) DidOpen(path, languageID, text string) {
	_ = c.EnsureOpened(path, languageID, text)
}

// DidChange informs the server that a document was edited. Versions bump
// monotonically per document; gopls ignores changes that do not follow the
// last version it saw, and the editor fires these from many goroutines.
func (c *Client) DidChange(path, text string, _ int) {
	if c.stdin == nil {
		// Stub or already-closed transport: nothing to notify, and bumping
		// bookkeeping for a session that never existed would be noise.
		return
	}
	abs, _ := filepath.Abs(path)
	c.mu.Lock()
	c.versions[abs]++
	v := c.versions[abs]
	c.mu.Unlock()
	_ = c.notify("textDocument/didChange", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":     pathToURI(abs),
			"version": v,
		},
		"contentChanges": []map[string]interface{}{
			{"text": text},
		},
	})
}

// Completion requests textDocument/completion at the given position.
// Returns an empty (non-nil) slice when the server has no suggestions.
func (c *Client) Completion(path string, line, col int) ([]CompletionItem, error) {
	abs, _ := filepath.Abs(path)
	res, err := c.call("textDocument/completion", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": pathToURI(abs),
		},
		"position": map[string]interface{}{
			"line":      line,
			"character": col,
		},
		"context": map[string]interface{}{"triggerKind": 1}, // Invoked
	})
	if err != nil || len(res) == 0 {
		return nil, err
	}

	// Result is either []CompletionItem or {isIncomplete, items:[...]}.
	var items []CompletionItem
	if err := json.Unmarshal(res, &items); err == nil && items != nil {
		return items, nil
	}
	var wrap struct {
		Items []CompletionItem `json:"items"`
	}
	if err := json.Unmarshal(res, &wrap); err == nil {
		return wrap.Items, nil
	}
	return nil, nil
}

// Definition requests the jump target for Go to Definition.
func (c *Client) Definition(path string, line, col int) (*Location, error) {
	abs, _ := filepath.Abs(path)
	res, err := c.call("textDocument/definition", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": pathToURI(abs),
		},
		"position": map[string]interface{}{
			"line":      line,
			"character": col,
		},
	})
	if err != nil || len(res) == 0 {
		return nil, err
	}

	// Definition can return Location or []Location
	var loc struct {
		URI   string `json:"uri"`
		Range struct {
			Start struct {
				Line      int `json:"line"`
				Character int `json:"character"`
			} `json:"start"`
		} `json:"range"`
	}
	if err := json.Unmarshal(res, &loc); err == nil && loc.URI != "" {
		return &Location{
			Path: uriToPath(loc.URI),
			Line: loc.Range.Start.Line,
			Col:  loc.Range.Start.Character,
		}, nil
	}

	var locs []struct {
		URI   string `json:"uri"`
		Range struct {
			Start struct {
				Line      int `json:"line"`
				Character int `json:"character"`
			} `json:"start"`
		} `json:"range"`
	}
	if err := json.Unmarshal(res, &locs); err == nil && len(locs) > 0 {
		return &Location{
			Path: uriToPath(locs[0].URI),
			Line: locs[0].Range.Start.Line,
			Col:  locs[0].Range.Start.Character,
		}, nil
	}

	return nil, nil
}

// TextEdit is one replacement inside a document. Positions are 0-based;
// StartCol/EndCol are LSP character offsets (UTF-16 code units, see
// ColToRune) because the protocol says so — convert at application time when
// the line's runes are available.
type TextEdit struct {
	StartLine int
	StartCol  int
	EndLine   int
	EndCol    int
	NewText   string
}

// WorkspaceEdit is the subset of LSP WorkspaceEdit the editor applies: the
// per-file edit lists, flattened from both the "changes" and
// "documentChanges" reply shapes.
type WorkspaceEdit struct {
	// Files maps an absolute path (URI already resolved) to its edits.
	Files map[string][]TextEdit
}

// CodeAction is one quick fix / refactor the server offers for a position.
type CodeAction struct {
	Title string
	Kind  string
	// Edit is nil for command-only actions: a plain editor has no command
	// dispatcher yet, so they are shown but refuse to run.
	Edit *WorkspaceEdit
}

// ColToRune converts an LSP character offset (UTF-16 code units) into a rune
// column of the given line. Beyond the BMP one rune is two UTF-16 units.
func ColToRune(line []rune, col16 int) int {
	if col16 <= 0 {
		return 0
	}
	units := 0
	for i, r := range line {
		if units >= col16 {
			return i
		}
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
	}
	return len(line)
}

// SortedEdits returns the edits sorted right-to-left, so applying them one by
// one never shifts a later position.
func SortedEdits(edits []TextEdit) []TextEdit {
	out := make([]TextEdit, len(edits))
	copy(out, edits)
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.StartLine != b.StartLine {
			return a.StartLine > b.StartLine
		}
		return a.StartCol > b.StartCol
	})
	return out
}

// ApplyEditsToText applies the edits onto raw document text (they are sorted
// right-to-left inside) and returns the result. Handles multi-line ranges and
// multi-line insertions.
func ApplyEditsToText(text string, edits []TextEdit) string {
	lines := strings.Split(text, "\n")
	// \r stays part of its line: the server's positions do not count it
	// either, so line splits on "\n" keep positions aligned.
	for _, e := range SortedEdits(edits) {
		if e.StartLine < 0 || e.StartLine >= len(lines) || e.EndLine < e.StartLine || e.EndLine >= len(lines) {
			continue
		}
		sl, el := []rune(lines[e.StartLine]), []rune(lines[e.EndLine])
		sc := ColToRune(sl, e.StartCol)
		ec := ColToRune(el, e.EndCol)
		if sc > len(sl) || ec > len(el) {
			continue
		}
		ins := strings.Split(e.NewText, "\n")
		out := make([]string, 0, len(ins))
		for k, part := range ins {
			// Case order matters: with a single-line replacement both the
			// first and the last part are k == 0.
			switch {
			case k == 0 && k == len(ins)-1:
				out = append(out, string(sl[:sc])+part+string(el[ec:]))
			case k == 0:
				out = append(out, string(sl[:sc])+part)
			case k == len(ins)-1:
				out = append(out, part+string(el[ec:]))
			default:
				out = append(out, part)
			}
		}
		newLines := make([]string, 0, len(lines)-(e.EndLine-e.StartLine)+len(ins))
		newLines = append(newLines, lines[:e.StartLine]...)
		newLines = append(newLines, out...)
		newLines = append(newLines, lines[e.EndLine+1:]...)
		lines = newLines
	}
	return strings.Join(lines, "\n")
}

type rawEdit struct {
	Range struct {
		Start struct {
			Line      int `json:"line"`
			Character int `json:"character"`
		} `json:"start"`
		End struct {
			Line      int `json:"line"`
			Character int `json:"character"`
		} `json:"end"`
	} `json:"range"`
	NewText string `json:"newText"`
}

// parseWorkspaceEdit flattens the two WorkspaceEdit reply shapes: the plain
// "changes" map and "documentChanges" with a versioned textDocument.
func parseWorkspaceEdit(res json.RawMessage) *WorkspaceEdit {
	var raw struct {
		Changes         map[string][]rawEdit `json:"changes"`
		DocumentChanges []struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Edits []rawEdit `json:"edits"`
		} `json:"documentChanges"`
	}
	if err := json.Unmarshal(res, &raw); err != nil {
		return nil
	}
	out := &WorkspaceEdit{Files: map[string][]TextEdit{}}
	add := func(uri string, edits []rawEdit) {
		if uri == "" || len(edits) == 0 {
			return
		}
		p := uriToPath(uri)
		for _, e := range edits {
			out.Files[p] = append(out.Files[p], TextEdit{
				StartLine: e.Range.Start.Line,
				StartCol:  e.Range.Start.Character,
				EndLine:   e.Range.End.Line,
				EndCol:    e.Range.End.Character,
				NewText:   e.NewText,
			})
		}
	}
	for uri, edits := range raw.Changes {
		add(uri, edits)
	}
	for _, dc := range raw.DocumentChanges {
		add(dc.TextDocument.URI, dc.Edits)
	}
	if len(out.Files) == 0 {
		return nil
	}
	return out
}

// Rename asks the server for the edits that rename the symbol at (line, col).
func (c *Client) Rename(path string, line, col int, newName string) (*WorkspaceEdit, error) {
	abs, _ := filepath.Abs(path)
	res, err := c.call("textDocument/rename", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": pathToURI(abs),
		},
		"position": map[string]interface{}{
			"line":      line,
			"character": col,
		},
		"newName": newName,
	})
	if err != nil || len(res) == 0 {
		return nil, err
	}
	return parseWorkspaceEdit(res), nil
}

// CodeActions returns the quick fixes and refactors the server offers at
// (line, col); a bare position works as a zero-width range there.
func (c *Client) CodeActions(path string, line, col int) ([]CodeAction, error) {
	abs, _ := filepath.Abs(path)
	pos := map[string]int{"line": line, "character": col}
	res, err := c.call("textDocument/codeAction", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": pathToURI(abs),
		},
		"range": map[string]interface{}{
			"start": pos,
			"end":   pos,
		},
		"context": map[string]interface{}{
			"diagnostics": []interface{}{},
		},
	})
	if err != nil || len(res) == 0 {
		return nil, err
	}
	var raws []struct {
		Title string          `json:"title"`
		Kind  string          `json:"kind"`
		Edit  json.RawMessage `json:"edit"`
	}
	if err := json.Unmarshal(res, &raws); err != nil {
		return nil, err
	}
	var out []CodeAction
	for _, r := range raws {
		ca := CodeAction{Title: r.Title, Kind: r.Kind}
		if len(r.Edit) > 0 && string(r.Edit) != "null" {
			ca.Edit = parseWorkspaceEdit(r.Edit)
		}
		out = append(out, ca)
	}
	return out, nil
}
