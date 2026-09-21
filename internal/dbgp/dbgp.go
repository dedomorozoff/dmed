// Package dbgp implements a DBGp protocol client for step-debugging engines
// that speak the legacy XML wire protocol — most notably Xdebug (PHP), which
// does not support DAP. Xdebug initiates the connection: the client listens on
// a TCP port (9003 by default), the PHP process dials it during a debug
// session, and both sides exchange NUL-terminated XML documents.
//
// The client exposes the same method surface as internal/dap.Client so the
// editor can drive Xdebug through the existing debug panel. Data is returned
// in the shared dap data shapes and events as dap.Event, keeping the TUI free
// of DBGp specifics.
package dbgp

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dmed/internal/dap"
	"dmed/internal/debug"
)

// dbgpNode is a decoded DBGp XML document. Text (including CDATA) lands in
// text, child elements in kids, attributes in attrs.
type dbgpNode struct {
	name  string
	attrs map[string]string
	text  string
	kids  []*dbgpNode
}

// parseDoc decodes one DBGp document (the bytes readMessage returned, with the
// framing already stripped) into a node tree. The document element lands in the
// returned node; a transient container node is used internally so the reader
// loop can detect a clean end of input.
func parseDoc(data []byte) (*dbgpNode, error) {
	container := &dbgpNode{}
	dec := xml.NewDecoder(bytes.NewReader(data))
	// Xdebug declares iso-8859-1 in the XML prolog of every document it sends;
	// Go's decoder rejects a declared charset it has no reader for, so without
	// this it fails on every real document (the in-memory mock documents used
	// to omit the prolog, which hid the problem).
	dec.CharsetReader = charsetReader
	if err := visit(dec, container); err != nil {
		return nil, err
	}
	if len(container.kids) == 0 {
		return nil, fmt.Errorf("dbgp: empty document")
	}
	return container.kids[0], nil
}

// charsetReader supplies Go's XML decoder with a reader for the charsets DBGp
// engines declare. Xdebug emits iso-8859-1, a single-byte charset: since the
// document is already fully in memory, widen each byte to its code point and
// hand back a UTF-8 reader. Any other charset passes through untouched (the
// decoder handles UTF-8 natively).
func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(charset)) {
	case "iso-8859-1", "iso8859-1", "latin-1", "latin1":
		raw, err := io.ReadAll(input)
		if err != nil {
			return nil, err
		}
		var out bytes.Buffer
		out.Grow(len(raw) * 2)
		for _, b := range raw {
			out.WriteRune(rune(b))
		}
		return bytes.NewReader(out.Bytes()), nil
	default:
		return input, nil
	}
}

// visit walks XML tokens, filling children/text into n until its end element.
func visit(d *xml.Decoder, n *dbgpNode) error {
	for {
		tok, err := d.Token()
		if err != nil {
			if err == io.EOF {
				return nil // clean end of a full document
			}
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			child := &dbgpNode{name: t.Name.Local, attrs: attrMap(t.Attr)}
			n.kids = append(n.kids, child)
			if err := visit(d, child); err != nil {
				return err
			}
		case xml.CharData:
			n.text += string(t)
		case xml.EndElement:
			return nil
		}
	}
}

func attrMap(a []xml.Attr) map[string]string {
	m := make(map[string]string, len(a))
	for _, at := range a {
		m[at.Name.Local] = at.Value
	}
	return m
}

func (n *dbgpNode) attr(name string) string {
	if n == nil {
		return ""
	}
	return n.attrs[name]
}

func (n *dbgpNode) child(name string) *dbgpNode {
	if n == nil {
		return nil
	}
	for _, k := range n.kids {
		if k.name == name {
			return k
		}
	}
	return nil
}

func (n *dbgpNode) children(name string) []*dbgpNode {
	if n == nil {
		return nil
	}
	out := []*dbgpNode{}
	for _, k := range n.kids {
		if k.name == name {
			out = append(out, k)
		}
	}
	return out
}

// readMessage returns the next DBGp document. The debugger engine frames every
// document it sends over the socket as an ASCII byte count, a NUL, the data,
// and a NUL ("511\0<init .../>\0"), so both the length frame and the data's
// own terminator are consumed here: callers only ever see the XML. Reading
// until the first NUL instead would hand the length ("511") back as if it were
// a document, which is exactly what broke the handshake against real Xdebug.
// IDE -> engine commands are unframed; see sendText.
func readMessage(r *bufio.Reader) ([]byte, error) {
	head, err := readUntilNUL(r)
	if err != nil {
		return nil, err
	}
	size, err := strconv.Atoi(strings.TrimSpace(string(head)))
	if err != nil || size < 0 {
		return nil, fmt.Errorf("dbgp: bad frame length %q", strings.TrimSpace(string(head)))
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}
	if b, err := r.ReadByte(); err != nil {
		return nil, err
	} else if b != 0 {
		return nil, fmt.Errorf("dbgp: frame not NUL-terminated")
	}
	if dbgpDebug {
		fmt.Fprintf(os.Stderr, "dbgp << %s\n", data)
	}
	return data, nil
}

// readUntilNUL reads bytes up to (and excluding) the next NUL, returning the
// io.EOF of the underlying reader when the stream ends first.
func readUntilNUL(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		b, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		if b == 0 {
			return buf, nil
		}
		buf = append(buf, b)
	}
}

// callResult is the outcome of one synchronous command delivered to its waiter.
type callResult struct {
	node *dbgpNode
	err  error
}

// varRef is what a variables-tree reference points at: a variable context of a
// stack frame, or a named property of one.
type varRef struct {
	level     int
	contextID int // >= 0 for context_get; -1 for property_get
	fullname  string
}

// Client is a connected DBGp session. Methods mirror the dap.Client surface the
// editor's debug panel is built on.
type Client struct {
	conn    net.Conn
	r       *bufio.Reader
	ln      net.Listener
	php     *exec.Cmd
	onEvent func(dap.Event)

	appid       int64
	stopOnEntry bool

	nextID       int64
	writeMu      sync.Mutex
	mu           sync.Mutex // guards pending, continuation, breakpoints, refs
	pending      map[int64]chan *callResult
	continuation map[int64]bool
	breakpoints  map[string]map[int]int64
	refs         map[int64]varRef
	nextRef      int64

	emittedExit bool
	closed      int32
}

const callTimeout = 60 * time.Second

// dbgpDebug traces the DBGp wire on stderr when DMED_DBGP_DEBUG is set, the
// way DMED_DEBUG_KEYS traces key handling: it is the only way to see what a
// real engine actually sends when a session misbehaves.
var dbgpDebug = os.Getenv("DMED_DBGP_DEBUG") != ""

func newClient(ln net.Listener, php *exec.Cmd, onEvent func(dap.Event)) *Client {
	return &Client{
		ln:           ln,
		php:          php,
		onEvent:      onEvent,
		pending:      make(map[int64]chan *callResult),
		continuation: make(map[int64]bool),
		breakpoints:  make(map[string]map[int]int64),
		refs:         make(map[int64]varRef),
	}
}

func (c *Client) event(ev dap.Event) {
	if c.onEvent != nil && atomic.LoadInt32(&c.closed) == 0 {
		c.onEvent(ev)
	}
}

func (c *Client) Seq() int64 {
	return atomic.AddInt64(&c.nextID, 1)
}

// sendText writes a DBGp command, NUL-terminated like the engine documents.
func (c *Client) sendText(cmd string) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if dbgpDebug {
		fmt.Fprintf(os.Stderr, "dbgp >> %s\n", cmd)
	}
	if c.conn == nil {
		return fmt.Errorf("dbgp: not connected")
	}
	_, err := io.WriteString(c.conn, cmd+"\x00")
	return err
}

// call sends a command with a fresh transaction id appended (unless cmd is an
// eval-style command that embeds `-- expr` after the id) and waits for the
// matching response. The waiter channel is handed to await by reference so an
// in-flight response that routes before await starts still lands in it.
func (c *Client) call(cmd string, seq int64) (*dbgpNode, error) {
	ch := make(chan *callResult, 1)
	c.mu.Lock()
	c.pending[seq] = ch
	c.mu.Unlock()
	if err := c.sendText(fmt.Sprintf("%s -i %d", cmd, seq)); err != nil {
		c.dropPending(seq)
		return nil, err
	}
	return c.await(seq, ch, cmd)
}

// eval evaluates an expression; the expression trails the transaction id, so
// the command is built here rather than by call(). The engine expects the
// expression base64-encoded: Xdebug 3.4 decodes it as such, and a verbatim
// expression is read as garbage ("$a + $b" evaluated to a constant named
// "k\xe6"). Note the engine always evaluates in the current stop frame, so the
// caller's frameID cannot influence it.
func (c *Client) eval(expr string) (*dbgpNode, error) {
	seq := c.Seq()
	cmd := fmt.Sprintf("eval -i %d -- %s", seq,
		base64.StdEncoding.EncodeToString([]byte(expr)))
	ch := make(chan *callResult, 1)
	c.mu.Lock()
	c.pending[seq] = ch
	c.mu.Unlock()
	if err := c.sendText(cmd); err != nil {
		c.dropPending(seq)
		return nil, fmt.Errorf("dbgp eval: %w", err)
	}
	return c.await(seq, ch, cmd)
}

func (c *Client) await(seq int64, ch chan *callResult, cmd string) (*dbgpNode, error) {
	select {
	case res := <-ch:
		return res.node, res.err
	case <-time.After(callTimeout):
		c.dropPending(seq)
		return nil, fmt.Errorf("dbgp: %s timed out", cmd)
	}
}

func (c *Client) dropPending(seq int64) {
	c.mu.Lock()
	delete(c.pending, seq)
	c.mu.Unlock()
}

// continueCmd sends a continuation command (run/step_into/...) without waiting
// for its response: the next stop arrives asynchronously as an EventStopped.
// This mirrors DAP's async model, where launch returns promptly.
func (c *Client) continueCmd(command string) error {
	seq := c.Seq()
	c.mu.Lock()
	c.continuation[seq] = true
	c.mu.Unlock()
	// DBGp has no "continued" notification to mirror DAP's, so synthesize one.
	// It MUST be emitted before the command is sent: once the command is on the
	// wire the read loop can process the following stop response and emit an
	// EventStopped that races past Continued. In a real session that reorder
	// drops the stopped->running transition (the panel stays "stopped" while the
	// debuggee is actually running, so F5 never resumes a launch and the first
	// breakpoint hit reads as "failed to launch").
	c.event(dap.Event{Kind: dap.EventContinued})
	if err := c.sendText(fmt.Sprintf("%s -i %d", command, seq)); err != nil {
		c.mu.Lock()
		delete(c.continuation, seq)
		c.mu.Unlock()
		return err
	}
	return nil
}

// readLoop is the single reader of the socket.
func (c *Client) readLoop() {
	for {
		data, err := readMessage(c.r)
		if err != nil {
			c.disconnected()
			return
		}
		c.handleDoc(data)
	}
}

// drainInit consumes the first document after Xdebug dials in: the <init>
// carrying the app id and entry file. It blocks only briefly.
func (c *Client) drainInit() (*dbgpNode, error) {
	data, err := readMessage(c.r)
	if err != nil {
		return nil, err
	}
	return parseDoc(data)
}

// handleDoc routes one decoded document.
func (c *Client) handleDoc(data []byte) {
	n, err := parseDoc(data)
	if err != nil {
		return
	}
	switch n.name {
	case "init":
		c.applyInit(n)
	case "stream", "notify":
		c.emitOutput(n)
	case "response":
		c.routeResponse(n)
	default:
		// engine greeting / connection errors carry no state for us
	}
}

func (c *Client) routeResponse(n *dbgpNode) {
	tid, _ := strconv.ParseInt(n.attr("transaction_id"), 10, 64)
	c.mu.Lock()
	_, isCont := c.continuation[tid]
	if isCont {
		delete(c.continuation, tid)
	}
	ch, ok := c.pending[tid]
	if ok {
		delete(c.pending, tid)
	}
	c.mu.Unlock()

	if isCont {
		c.handleContinuation(n)
		return
	}
	if !ok {
		return // response for a timed-out request
	}
	// Engines report failure either with success="0" or with an <error> child
	// and no success attribute at all — Xdebug 3.4 does the latter, so keying
	// off success alone silently turned errors into empty successes.
	if n.attr("success") == "0" || n.child("error") != nil {
		ch <- &callResult{err: fmt.Errorf("dbgp %s: %s", n.attr("command"), errorMessage(n))}
		return
	}
	ch <- &callResult{node: n}
}

// handleContinuation turns a run/step response into the events the editor
// consumes: embedded output first, then a stopped or exited event.
func (c *Client) handleContinuation(n *dbgpNode) {
	c.emitOutput(n)
	switch n.attr("status") {
	case "break":
		ev := dap.Event{
			Kind:     dap.EventStopped,
			Reason:   stopReason(n.attr("reason")),
			ThreadID: c.appid,
		}
		if msg := findMessage(n); msg != nil {
			ev.SourcePath = uriToPath(msg.attr("filename"))
			ev.Line, _ = strconv.Atoi(msg.attr("lineno"))
		}
		c.event(ev)
	case "stopping":
		// The engine reports the program finishing but keeps the socket open
		// waiting for `stop` (Xdebug does), so the end of the session has to be
		// announced here rather than on connection close — otherwise the panel
		// would sit in "running" until the user tore the session down.
		if !c.emittedExit {
			c.emittedExit = true
			c.event(dap.Event{Kind: dap.EventExited})
		}
		c.event(dap.Event{Kind: dap.EventTerminated})
	}
}

// findMessage locates the <message> element of a stop response (Xdebug marks
// it with the file/line it stopped at).
func findMessage(n *dbgpNode) *dbgpNode {
	if n == nil {
		return nil
	}
	for _, k := range n.kids {
		if k.name == "message" && k.attr("filename") != "" {
			return k
		}
	}
	return nil
}

func stopReason(r string) string {
	switch r {
	case "ok", "":
		return "breakpoint"
	case "exception", "error":
		return "exception"
	default:
		return r
	}
}

// emitOutput surfaces <stream type="stdout|stderr"> content, whether the
// stream is a root document or nested inside a response.
func (c *Client) emitOutput(n *dbgpNode) {
	if n == nil {
		return
	}
	for _, s := range n.children("stream") {
		text := decodeText(s)
		if strings.TrimSpace(text) == "" {
			continue
		}
		cat := s.attr("type")
		if cat != "stdout" && cat != "stderr" {
			cat = "stdout"
		}
		c.event(dap.Event{Kind: dap.EventOutput, OutputCat: cat, Output: text})
	}
	if n.name == "stream" {
		text := decodeText(n)
		if strings.TrimSpace(text) != "" {
			c.event(dap.Event{Kind: dap.EventOutput, OutputCat: "stdout", Output: text})
		}
	}
}

// decodeText resolves a property/stream value: Xdebug base64-encodes content
// that needs it, everything else is plain text.
func decodeText(n *dbgpNode) string {
	if n == nil {
		return ""
	}
	if n.attr("encoding") == "base64" {
		if b, err := base64.StdEncoding.DecodeString(n.text); err == nil {
			return string(b)
		}
	}
	return n.text
}

// errorMessage extracts the <message> child of an <error> response element.
func errorMessage(n *dbgpNode) string {
	if e := n.child("error"); e != nil {
		if m := e.child("message"); m != nil {
			return strings.TrimSpace(m.text)
		}
		return "error code " + e.attr("code")
	}
	return "engine error"
}

// disconnected runs when the engine closes the connection or the child
// interpreter dies.
func (c *Client) disconnected() {
	if atomic.LoadInt32(&c.closed) != 0 {
		return
	}
	c.mu.Lock()
	for seq, ch := range c.pending {
		delete(c.pending, seq)
		ch <- &callResult{err: fmt.Errorf("dbgp: connection closed")}
	}
	c.mu.Unlock()
	// Once the engine has reported the program stopping the session is over:
	// a close after that is just the socket being reaped, not a lost
	// connection, so it must not drag the panel back from "ended" to idle.
	if !c.emittedExit {
		c.emittedExit = true
		c.event(dap.Event{Kind: dap.EventExited})
		c.event(dap.Event{Kind: dap.EventDisconnected})
	}
	if c.php != nil && c.php.Process != nil {
		_ = c.php.Process.Kill()
	}
}

// newRef books a variables-tree reference and returns it.
func (c *Client) newRef(vr varRef) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextRef++
	c.refs[c.nextRef] = vr
	return c.nextRef
}

// Initialize negotiates the engine features the panel relies on. It returns
// true so the editor sends ConfigureDone, which for DBGp means "start running".
func (c *Client) Initialize() (bool, error) {
	for _, f := range [][2]string{{"extended_properties", "1"}, {"show_hidden", "1"}} {
		if _, err := c.call("feature_set -n "+f[0]+" -v "+f[1], c.Seq()); err != nil {
			return true, err
		}
	}
	return true, nil
}

// Launch records the launch configuration. The interpreter process is spawned
// before the client exists (StartDebugger), so there is no engine request here.
func (c *Client) Launch(args map[string]interface{}) error {
	if soe, ok := args["stopOnEntry"].(bool); ok {
		c.stopOnEntry = soe
	}
	return nil
}

// ConfigureDone starts execution: step on the entry point when requested,
// otherwise run to the first breakpoint.
func (c *Client) ConfigureDone() error {
	if c.stopOnEntry {
		return c.continueCmd("step_into")
	}
	return c.continueCmd("run")
}

// pathToURI converts a local path to the file:// URI Xdebug matches
// breakpoints against (file:///C:/x.php or file:///home/x.php).
func pathToURI(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	u := "file:///"
	if vol := filepath.VolumeName(abs); vol != "" {
		u = "file:///" + filepath.ToSlash(abs)
	} else {
		u += filepath.ToSlash(abs)
	}
	return strings.ReplaceAll(u, " ", "%20")
}

// uriToPath converts a file:// URI back to a local path.
func uriToPath(u string) string {
	if !strings.HasPrefix(u, "file://") {
		return u
	}
	rest := strings.TrimPrefix(u, "file://")
	rest = strings.TrimPrefix(rest, "/")
	rest = strings.ReplaceAll(rest, "%20", " ")
	return filepath.FromSlash(rest)
}

// SetBreakpoints installs the given lines of a file, removing lines that are
// no longer wanted, and reports verification for every requested line.
func (c *Client) SetBreakpoints(path string, lines []int) ([]dap.Breakpoint, error) {
	want := map[int]bool{}
	for _, l := range lines {
		want[l] = true
	}
	uri := pathToURI(path)

	var out []dap.Breakpoint
	for _, l := range lines {
		c.mu.Lock()
		_, installed := c.breakpoints[path][l]
		c.mu.Unlock()
		if installed {
			out = append(out, dap.Breakpoint{Verified: true, Line: l})
			continue
		}
		id, err := c.setBreakpoint(uri, l)
		if err != nil {
			out = append(out, dap.Breakpoint{Verified: false, Line: l, Message: err.Error()})
			continue
		}
		c.mu.Lock()
		if c.breakpoints[path] == nil {
			c.breakpoints[path] = map[int]int64{}
		}
		c.breakpoints[path][l] = id
		c.mu.Unlock()
		out = append(out, dap.Breakpoint{Verified: true, Line: l})
	}

	// Remove breakpoints the user dropped since the last sync.
	c.mu.Lock()
	var stale []int64
	var staleLines []int
	for l, id := range c.breakpoints[path] {
		if !want[l] {
			stale = append(stale, id)
			staleLines = append(staleLines, l)
		}
	}
	for _, sl := range staleLines {
		delete(c.breakpoints[path], sl)
	}
	if len(c.breakpoints[path]) == 0 {
		delete(c.breakpoints, path)
	}
	c.mu.Unlock()
	for _, id := range stale {
		_, _ = c.call("breakpoint_remove -d "+strconv.FormatInt(id, 10), c.Seq())
	}
	return out, nil
}

func (c *Client) setBreakpoint(uri string, line int) (int64, error) {
	res, err := c.call(fmt.Sprintf("breakpoint_set -t line -f %s -n %d", uri, line), c.Seq())
	if err != nil {
		return 0, err
	}
	// The DBGp spec puts the new breakpoint id on the response element itself
	// (<response command="breakpoint_set" state="enabled" id="20001"/>), which
	// is what Xdebug sends; there is no <breakpoint> child.
	id, err := strconv.ParseInt(res.attr("id"), 10, 64)
	if err != nil || id == 0 {
		return 0, fmt.Errorf("malformed breakpoint id %q", res.attr("id"))
	}
	return id, nil
}

// Continue resumes the stopped program.
func (c *Client) Continue(threadID int64) error {
	return c.continueCmd("run")
}

// Pause cannot interrupt a running CLI PHP script over DBGp; it is a no-op so
// F5 never deadlocks.
func (c *Client) Pause(threadID int64) error {
	return nil
}

// Next, StepIn and StepOut step within the stopped program.
func (c *Client) Next(threadID int64) error    { return c.continueCmd("step_over") }
func (c *Client) StepIn(threadID int64) error  { return c.continueCmd("step_into") }
func (c *Client) StepOut(threadID int64) error { return c.continueCmd("step_out") }

// Threads reports the single debuggee thread (DBGp has no thread model).
func (c *Client) Threads() ([]dap.Thread, error) {
	id := c.appid
	if id < 1 {
		id = 1
	}
	return []dap.Thread{{ID: id, Name: "php"}}, nil
}

// StackTrace reads the call stack. Frame IDs are the stack levels.
func (c *Client) StackTrace(threadID int64, levels int) ([]dap.StackFrame, error) {
	res, err := c.call("stack_get", c.Seq())
	if err != nil {
		return nil, err
	}
	stacks := res.children("stack")
	if levels > 0 && len(stacks) > levels {
		stacks = stacks[:levels]
	}
	frames := make([]dap.StackFrame, 0, len(stacks))
	for _, s := range stacks {
		level, _ := strconv.Atoi(s.attr("level"))
		ln, _ := strconv.Atoi(s.attr("lineno"))
		frames = append(frames, dap.StackFrame{
			ID:   int64(level),
			Name: s.attr("where"),
			Path: uriToPath(s.attr("filename")),
			Line: ln,
		})
	}
	return frames, nil
}

// Scopes lists the variable contexts of a stack frame (the frame ID is the
// stack level).
func (c *Client) Scopes(frameID int64) ([]dap.Scope, error) {
	level := int(frameID)
	res, err := c.call("context_names -d "+strconv.Itoa(level), c.Seq())
	if err != nil {
		return nil, err
	}
	scopes := []dap.Scope{}
	for _, ctx := range res.children("context") {
		ctxID, err := strconv.Atoi(ctx.attr("id"))
		if err != nil {
			continue
		}
		ref := c.newRef(varRef{level: level, contextID: ctxID})
		scopes = append(scopes, dap.Scope{Name: ctx.attr("name"), VariablesRef: ref})
	}
	return scopes, nil
}

// Variables expands a scope or a compound variable.
func (c *Client) Variables(ref int64) ([]dap.Variable, error) {
	c.mu.Lock()
	vr, ok := c.refs[ref]
	c.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("dbgp: unknown variable reference %d", ref)
	}
	var res *dbgpNode
	var err error
	if vr.contextID >= 0 {
		res, err = c.call(fmt.Sprintf("context_get -d %d -c %d", vr.level, vr.contextID), c.Seq())
	} else {
		res, err = c.call("property_get -d "+strconv.Itoa(vr.level)+" -n "+xmlAttr(vr.fullname), c.Seq())
	}
	if err != nil {
		return nil, err
	}
	props := res.children("property")
	if vr.contextID < 0 && len(props) == 1 {
		// property_get returns the requested variable as a single property;
		// its children are the rows to show.
		props = props[0].children("property")
	}
	vars := make([]dap.Variable, 0, len(props))
	for _, p := range props {
		childRef := int64(0)
		if hasChildren(p) {
			childRef = c.newRef(varRef{level: vr.level, contextID: -1, fullname: p.attr("fullname")})
		}
		vars = append(vars, dap.Variable{
			Name:         p.attr("name"),
			Value:        propValue(p),
			VariablesRef: childRef,
			Type:         p.attr("type"),
		})
	}
	return vars, nil
}

// hasChildren reports whether a property can be expanded in the variables
// panel. Xdebug sets children="1" on any compound value and puts the real
// count in numchildren; the DBGp spec's plain children="N" form also works.
func hasChildren(p *dbgpNode) bool {
	for _, a := range []string{"numchildren", "children"} {
		if n := p.attr(a); n != "" && n != "0" {
			return true
		}
	}
	return false
}

// childCount renders the element count of a compound value. numchildren is
// Xdebug's real count; children is a flag there but a count per the DBGp spec,
// so it is only the fallback.
func childCount(p *dbgpNode) string {
	if n := p.attr("numchildren"); n != "" {
		return n
	}
	if n := p.attr("children"); n != "" {
		return n
	}
	return "0"
}

// propValue renders a property for the panel: a short summary for compound
// types, the decoded scalar text otherwise.
func propValue(p *dbgpNode) string {
	switch p.attr("type") {
	case "array":
		return "array(" + childCount(p) + ")"
	case "object":
		return "object(" + p.attr("class") + ")"
	default:
		return decodeText(p)
	}
}

// Evaluate runs an expression in the current stopped context.
func (c *Client) Evaluate(expr string, frameID int64) (dap.Variable, error) {
	res, err := c.eval(expr)
	if err != nil {
		return dap.Variable{}, err
	}
	if res.attr("success") == "0" {
		return dap.Variable{}, fmt.Errorf("eval: %s", errorMessage(res))
	}
	p := res.child("property")
	if p == nil {
		return dap.Variable{}, fmt.Errorf("eval: no property in response")
	}
	level := int(frameID)
	if level < 0 {
		level = 0
	}
	childRef := int64(0)
	if hasChildren(p) {
		childRef = c.newRef(varRef{level: level, contextID: -1, fullname: p.attr("fullname")})
	}
	return dap.Variable{Name: expr, Value: propValue(p), VariablesRef: childRef, Type: p.attr("type")}, nil
}

// Close tears the session down: best-effort stop, then close the socket and
// kill the child interpreter.
func (c *Client) Close() {
	if atomic.AddInt32(&c.closed, 1) > 1 {
		return
	}
	seq := c.Seq()
	_ = c.sendText(fmt.Sprintf("stop -i %d", seq))
	if c.conn != nil {
		_ = c.conn.Close()
	}
	if c.ln != nil {
		_ = c.ln.Close()
	}
	if c.php != nil && c.php.Process != nil {
		_ = c.php.Process.Kill()
	}
}

// applyInit records the <init> app id and announces the session, matching what
// handleDoc does for in-band init documents (start consumes init directly).
func (c *Client) applyInit(n *dbgpNode) {
	c.appid, _ = strconv.ParseInt(n.attr("appid"), 10, 64)
	c.event(dap.Event{Kind: dap.EventInitialized, ProcessID: int(c.appid), ProcessName: n.attr("language")})
}

// start wires an accepted connection and consumes the <init> handshake before
// the read loop starts, so there is never more than one reader of the stream.
func (c *Client) start(conn net.Conn) (*dbgpNode, error) {
	c.conn = conn
	c.r = bufio.NewReader(conn)
	init, err := c.drainInit()
	if err != nil {
		return nil, err
	}
	if init.name != "init" {
		return nil, fmt.Errorf("dbgp: expected <init>, received <%s>", init.name)
	}
	c.applyInit(init)
	c.attachConn(conn)
	return init, nil
}

// attachConn wires the accepted connection and starts the read loop.
func (c *Client) attachConn(conn net.Conn) {
	c.conn = conn
	c.r = bufio.NewReader(conn)
	go debug.CapturePanicReport(c.readLoop)
}

// StartDebugger launches an interpreter that hosts a DBGp engine (PHP with
// Xdebug), listens on addr for the engine's connect-back, and returns a
// ready client once the <init> document arrives. phpArgs trail program; the
// interpreter runs with XDEBUG_CONFIG+session set so Xdebug dials THIS
// listener.
func StartDebugger(interp, program string, phpArgs []string, dir, addr string, onEvent func(dap.Event)) (*Client, error) {
	if strings.TrimSpace(addr) == "" {
		addr = "127.0.0.1:9003"
	}
	if strings.TrimSpace(interp) == "" {
		interp = "php"
	}
	host, port := splitAddr(addr)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dbgp listen %s: %w", addr, err)
	}
	if _, err := exec.LookPath(interp); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("dbgp: %s not found — install it or set [debug] adapter_cmd", interp)
	}

	cmd := exec.Command(interp, append([]string{program}, phpArgs...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"XDEBUG_CONFIG=client_host="+host+" client_port="+port,
		"XDEBUG_SESSION=1",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("dbgp: %w", err)
	}

	c := newClient(ln, cmd, onEvent)
	go debug.CapturePanicReport(c.streamScanner(stdout, "stdout"))
	go debug.CapturePanicReport(c.streamScanner(stderr, "stderr"))

	type acc struct {
		conn net.Conn
		err  error
	}
	accCh := make(chan acc, 1)
	go debug.CapturePanicReport(func() {
		conn, err := ln.Accept()
		accCh <- acc{conn: conn, err: err}
	})
	waitCh := make(chan error, 1)
	go debug.CapturePanicReport(func() { waitCh <- cmd.Wait() })

	var conn net.Conn
	select {
	case a := <-accCh:
		if a.err != nil {
			c.Close()
			return nil, fmt.Errorf("dbgp accept: %w", a.err)
		}
		_ = ln.Close()
		conn = a.conn
	case err := <-waitCh:
		c.Close()
		if err != nil {
			return nil, fmt.Errorf("dbgp: interpreter exited before connecting: %w", err)
		}
		return nil, fmt.Errorf("dbgp: interpreter exited before connecting")
	case <-time.After(20 * time.Second):
		c.Close()
		return nil, fmt.Errorf("dbgp: Xdebug did not connect on %s within 20s", addr)
	}

	if _, err := c.start(conn); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// streamScanner surfaces the interpreter's stdout/stderr as output events.
// While a DBGp session is active Xdebug routes script output through <stream>
// documents, so these pipes usually stay quiet.
func (c *Client) streamScanner(r io.Reader, kind string) func() {
	return func() {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			c.event(dap.Event{Kind: dap.EventOutput, OutputCat: kind, Output: sc.Text()})
		}
	}
}

// splitAddr parses host:port, defaulting the host to 127.0.0.1.
func splitAddr(addr string) (host, port string) {
	host, port = "127.0.0.1", strings.TrimSpace(addr)
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		host = strings.TrimSpace(addr[:i])
		port = strings.TrimSpace(addr[i+1:])
	}
	if port == "" {
		port = "9003"
	}
	return host, port
}

// xmlAttr escapes a value for embedding in an XML attribute.
func xmlAttr(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}
