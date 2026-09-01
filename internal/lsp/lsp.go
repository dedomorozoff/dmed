package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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
	pending     map[int64]chan json.RawMessage
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
		pending:     make(map[int64]chan json.RawMessage),
		diagnostics: make(map[string][]Diagnostic),
		onDiag:      onDiag,
	}

	go c.readLoop()
	go c.initialize()

	return c, nil
}

func pathToURI(path string) string {
	path = filepath.ToSlash(path)
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

// callTimeout bounds how long call blocks waiting for a server response.
const callTimeout = 6 * time.Second

func (c *Client) call(method string, params interface{}) (json.RawMessage, error) {
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
	c.mu.Lock()
	_, err = c.stdin.Write(append([]byte(header), data...))
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}

	select {
	case res := <-ch:
		return res, nil
	case <-time.After(callTimeout):
		return nil, fmt.Errorf("lsp: %s timed out", method)
	}
}

func (c *Client) notify(method string, params interface{}) error {
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
	c.mu.Lock()
	_, err = c.stdin.Write(append([]byte(header), data...))
	c.mu.Unlock()
	return err
}

// DidOpen informs the server that a document was opened.
func (c *Client) DidOpen(path, languageID, text string) {
	abs, _ := filepath.Abs(path)
	_ = c.notify("textDocument/didOpen", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":        pathToURI(abs),
			"languageId": languageID,
			"version":    1,
			"text":       text,
		},
	})
}

// DidChange informs the server that a document was edited.
func (c *Client) DidChange(path, text string, version int) {
	abs, _ := filepath.Abs(path)
	_ = c.notify("textDocument/didChange", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":     pathToURI(abs),
			"version": version,
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
