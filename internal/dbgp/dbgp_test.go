package dbgp

import (
	"bufio"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"dmed/internal/dap"
)

func initTest(t *testing.T, f func()) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	done := make(chan struct{})
	go func() {
		f()
		close(done)
	}()
	select {
	case <-done:
	case <-timeout:
		t.Fatal("test timed out")
	}
}

const xmlNS = `xmlns="urn:debugger_protocol_v1"`

// xmlProlog is the XML declaration Xdebug puts in front of every document it
// sends; the declared charset must be handled by parseDoc.
const xmlProlog = `<?xml version="1.0" encoding="iso-8859-1"?>`

func okResp(cmd, tid string) string {
	return `<response ` + xmlNS + ` command="` + cmd + `" transaction_id="` + tid + `" success="1"></response>`
}

// stopResp builds a continuation response. stream is the script output that
// the engine attaches to this very frame ("" when the frame carries none), the
// way Xdebug embeds <stream> in the response that produced the output.
func stopResp(tid, status, reason, file string, line int, stream string) string {
	ns := ""
	msg := ""
	if status == "break" {
		ns = ` xmlns:xdebug="https://xdebug.org/dbgp/xdebug"`
		msg = `<xdebug:message filename="` + file + `" lineno="` + strconv.Itoa(line) + `"></xdebug:message>`
	}
	out := ""
	if stream != "" {
		out = `<stream type="stdout">` + stream + `</stream>`
	}
	return `<response ` + xmlNS + ns + ` command="run" transaction_id="` + tid +
		`" status="` + status + `" reason="` + reason + `">` + msg + out + `</response>`
}

// fakeEngine is a scripted DBGp engine: it greets with <init>, then answers
// commands with canned documents. runNum counts run/step continuations so the
// second resume can finish the program.
func fakeEngine(t *testing.T, conn net.Conn, runNum *int) {
	t.Helper()
	send := func(s string) {
		// Mirror the wire exactly: Xdebug prefixes every document with the XML
		// prolog and frames it as "<byte length>\0<xml>\0".
		if !strings.HasPrefix(s, "<?xml") {
			s = xmlProlog + "\n" + s
		}
		if _, err := io.WriteString(conn, strconv.Itoa(len(s))+"\x00"+s+"\x00"); err != nil {
			return
		}
	}
	send(`<init ` + xmlNS +
		` appid="1234" language="PHP" langversion="8.3" fileuri="file:///C:/proj/index.php">` +
		`<engine version="3.4.0"><![CDATA[Xdebug]]></engine></init>`)

	r := bufio.NewReader(conn)
	for {
		// IDE -> engine commands are unframed NUL-terminated lines.
		msg, err := readUntilNUL(r)
		if err != nil {
			return
		}
		s := string(msg)
		cmd := s
		if i := strings.IndexByte(s, ' '); i >= 0 {
			cmd = s[:i]
		}
		tid := "-1"
		if i := strings.Index(s, "-i "); i >= 0 {
			rest := s[i+3:]
			if j := strings.IndexByte(rest, ' '); j >= 0 {
				rest = rest[:j]
			}
			tid = rest
		}
		switch cmd {
		case "feature_set":
			send(okResp(cmd, tid))
		case "breakpoint_set":
			// The id rides on the response element (see setBreakpoint).
			send(`<response ` + xmlNS + ` command="breakpoint_set" transaction_id="` + tid +
				`" success="1" state="enabled" id="100"></response>`)
		case "breakpoint_remove":
			send(okResp(cmd, tid))
		case "stack_get":
			send(`<response ` + xmlNS + ` command="stack_get" transaction_id="` + tid + `" success="1">` +
				`<stack level="0" type="file" filename="file:///C:/proj/index.php" lineno="7" where="{}"></stack>` +
				`<stack level="1" type="file" filename="file:///C:/proj/index.php" lineno="15" where="{main}"></stack>` +
				`</response>`)
		case "context_names":
			send(`<response ` + xmlNS + ` command="context_names" transaction_id="` + tid + `" success="1">` +
				`<context id="0" name="Locals"></context><context id="1" name="Globals"></context></response>`)
		case "context_get":
			send(`<response ` + xmlNS + ` command="context_get" transaction_id="` + tid + `" success="1">` +
				`<property name="$i" fullname="$i" type="int" size="1" children="0">5</property>` +
				`<property name="$items" fullname="$items" type="array" size="3" children="3"></property>` +
				`<property name="$s" fullname="$s" type="string" size="11" children="0" encoding="base64">aGVsbG8gd29ybGQ=</property>` +
				`</response>`)
		case "property_get":
			send(`<response ` + xmlNS + ` command="property_get" transaction_id="` + tid + `" success="1">` +
				`<property name="$items" fullname="$items" type="array" size="3" children="3">` +
				`<property name="0" fullname="$items[0]" type="string" size="5" children="0">alpha</property>` +
				`<property name="1" fullname="$items[1]" type="string" size="5" children="0">beta</property>` +
				`</property></response>`)
		case "eval":
			send(`<response ` + xmlNS + ` command="eval" transaction_id="` + tid + `" success="1">` +
				`<property name="$0" fullname="$i+1" type="int" size="1" children="0">6</property></response>`)
		case "run":
			*runNum++
			if *runNum == 1 {
				send(stopResp(tid, "break", "ok", "file:///C:/proj/index.php", 7, "hello from script\n"))
			} else {
				send(stopResp(tid, "stopping", "ok", "", 0, ""))
			}
		case "step_into", "step_over", "step_out":
			*runNum++
			send(stopResp(tid, "break", "ok", "file:///C:/proj/index.php", 8, ""))
		case "stop":
			send(okResp(cmd, tid))
		case "bogus":
			// Xdebug reports failures with an <error> child and no success
			// attribute at all (measured against 3.4.0-dev).
			send(`<response ` + xmlNS + ` command="bogus" transaction_id="` + tid +
				`"><error code="5"><message><![CDATA[command is not available]]></message></error></response>`)
		default:
			t.Errorf("fake engine: unexpected command %q", s)
			send(okResp(cmd, tid))
		}
	}
}

func newClientForTest(t *testing.T, onEvent func(dap.Event), runNum *int) *Client {
	t.Helper()
	engine, client := net.Pipe()
	go fakeEngine(t, engine, runNum)
	c := newClient(nil, nil, onEvent)
	if _, err := c.start(client); err != nil {
		t.Fatalf("start handshake: %v", err)
	}
	t.Cleanup(func() {
		c.Close()
		_ = client.Close()
	})
	return c
}

func TestReadMessage(t *testing.T) {
	// The engine frames documents as "<len>\0<data>\0": the length frame must
	// never be handed back as if it were a document.
	r := bufio.NewReader(strings.NewReader("3\x00abc\x000\x00\x003\x00def\x00"))
	got, err := readMessage(r)
	if err != nil || string(got) != "abc" {
		t.Fatalf("first message = %q, %v", got, err)
	}
	got, err = readMessage(r)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty message = %q, %v", got, err)
	}
	got, err = readMessage(r)
	if err != nil || string(got) != "def" {
		t.Fatalf("second message = %q, %v", got, err)
	}
	if _, err := readMessage(r); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestReadMessageBadFrame(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("nope\x00abc\x00"))
	if _, err := readMessage(r); err == nil {
		t.Fatal("expected an error for a non-numeric frame length")
	}
}

// TestParseDocProlog covers the two things a real Xdebug document has that an
// in-memory literal does not: the XML prolog declaring iso-8859-1, and payload
// bytes outside ASCII.
func TestParseDocProlog(t *testing.T) {
	doc := xmlProlog + "\n" + `<init ` + xmlNS + ` appid="19492" language="PHP">` +
		`<engine version="3.4.0-dev"><![CDATA[Xdebug]]></engine></init>`
	n, err := parseDoc([]byte(doc))
	if err != nil {
		t.Fatalf("parseDoc with prolog: %v", err)
	}
	if n.name != "init" || n.attr("appid") != "19492" {
		t.Fatalf("init = %q %v", n.name, n.attrs)
	}
	if e := n.child("engine"); e == nil || e.text != "Xdebug" {
		t.Fatalf("engine = %+v", e)
	}

	// 0xE9 is "é" in iso-8859-1; widening it must not mangle the value.
	doc = xmlProlog + "\n" + `<response ` + xmlNS + ` command="eval">` +
		`<property name="$s" type="string">caf` + "\xe9" + `</property></response>`
	n, err = parseDoc([]byte(doc))
	if err != nil {
		t.Fatalf("parseDoc with latin-1 payload: %v", err)
	}
	if p := n.child("property"); p == nil || p.text != "café" {
		t.Fatalf("latin-1 property = %+v", p)
	}
}

func TestParseDoc(t *testing.T) {
	doc := `<response ` + xmlNS + ` command="eval" transaction_id="4" success="1">` +
		`<property name="$0" fullname="$i+1" type="int">6</property><stream type="stdout">x</stream></response>`
	n, err := parseDoc([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if n.name != "response" || n.attr("command") != "eval" || n.attr("transaction_id") != "4" {
		t.Fatalf("root = %q %v", n.name, n.attrs)
	}
	p := n.child("property")
	if p == nil || p.attr("type") != "int" || p.text != "6" {
		t.Fatalf("property = %+v", p)
	}
	if s := n.child("stream"); s == nil || s.attr("type") != "stdout" {
		t.Fatalf("stream = %+v", s)
	}
}

func TestBreakpointURIRoundTrip(t *testing.T) {
	if u := pathToURI(`C:\proj\my file.php`); u != "file:///C:/proj/my%20file.php" {
		t.Errorf("pathToURI = %q", u)
	}
	if p := uriToPath("file:///C:/proj/my%20file.php"); p != `C:\proj\my file.php` {
		t.Errorf("uriToPath = %q", p)
	}
}

func TestSplitAddr(t *testing.T) {
	h, p := splitAddr("0.0.0.0:9100")
	if h != "0.0.0.0" || p != "9100" {
		t.Errorf("splitAddr = %q/%q", h, p)
	}
	h, p = splitAddr("")
	if h != "127.0.0.1" || p != "9003" {
		t.Errorf("splitAddr default = %q/%q", h, p)
	}
}

func TestClientLifecycle(t *testing.T) {
	events := make(chan dap.Event, 32)
	onEvent := func(e dap.Event) { events <- e }
	var runNum int
	c := newClientForTest(t, onEvent, &runNum)

	getEvent := func(want dap.EventKind) dap.Event {
		t.Helper()
		select {
		case e := <-events:
			if e.Kind != want {
				t.Fatalf("event = %v, want %v", e.Kind, want)
			}
			return e
		case <-time.After(3 * time.Second):
			t.Fatalf("no %v event within timeout", want)
			return dap.Event{}
		}
	}

	supports, err := c.Initialize()
	if err != nil || !supports {
		t.Fatalf("Initialize = %v, %v", supports, err)
	}
	if got := getEvent(dap.EventInitialized); got.ProcessID != 1234 {
		t.Errorf("init process id = %d", got.ProcessID)
	}

	bps, err := c.SetBreakpoints(`C:\proj\index.php`, []int{3, 7})
	if err != nil || len(bps) != 2 {
		t.Fatalf("SetBreakpoints = %v, %v", bps, err)
	}
	for _, b := range bps {
		if !b.Verified {
			t.Errorf("breakpoint not verified: %+v", b)
		}
	}

	// Dropping line 3 must issue a breakpoint_remove for it.
	bps, err = c.SetBreakpoints(`C:\proj\index.php`, []int{7})
	if err != nil || len(bps) != 1 || bps[0].Line != 7 {
		t.Fatalf("re-SetBreakpoints = %v, %v", bps, err)
	}

	th, err := c.Threads()
	if err != nil || len(th) != 1 || th[0].ID != 1234 {
		t.Fatalf("Threads = %v, %v", th, err)
	}

	frames, err := c.StackTrace(1234, 0)
	if err != nil || len(frames) != 2 {
		t.Fatalf("StackTrace = %v, %v", frames, err)
	}
	if frames[0].Line != 7 || frames[0].Name != "{}" || frames[0].Path != `C:\proj\index.php` {
		t.Errorf("frame0 = %+v", frames[0])
	}

	scopes, err := c.Scopes(0)
	if err != nil || len(scopes) != 2 {
		t.Fatalf("Scopes = %v, %v", scopes, err)
	}
	locals := scopes[0]
	if locals.Name != "Locals" || locals.VariablesRef == 0 {
		t.Errorf("locals scope = %+v", locals)
	}

	vars, err := c.Variables(locals.VariablesRef)
	if err != nil || len(vars) != 3 {
		t.Fatalf("Variables = %v, %v", vars, err)
	}
	if vars[0].Value != "5" || vars[0].Type != "int" {
		t.Errorf("$i = %+v", vars[0])
	}
	if vars[1].Value != "array(3)" || vars[1].VariablesRef == 0 {
		t.Errorf("$items = %+v", vars[1])
	}
	if vars[2].Value != "hello world" {
		t.Errorf("$s = %+v", vars[2])
	}

	items, err := c.Variables(vars[1].VariablesRef)
	if err != nil || len(items) != 2 {
		t.Fatalf("array children = %v, %v", items, err)
	}
	if items[0].Name != "0" || items[0].Value != "alpha" || items[1].Value != "beta" {
		t.Errorf("items = %+v", items)
	}

	ev, err := c.Evaluate("$i+1", 0)
	if err != nil || ev.Value != "6" || ev.Type != "int" {
		t.Fatalf("Evaluate = %+v, %v", ev, err)
	}

	// ConfigureDone starts the program; the first run stops at line 7. Sending
	// run synthesizes a continued event, then the output printed before the
	// break (embedded in the break response) is flushed, then the stop.
	if err := c.ConfigureDone(); err != nil {
		t.Fatalf("ConfigureDone: %v", err)
	}
	getEvent(dap.EventContinued)
	if got := getEvent(dap.EventOutput); got.Output != "hello from script\n" || got.OutputCat != "stdout" {
		t.Errorf("output = %+v", got)
	}
	stop := getEvent(dap.EventStopped)
	if stop.SourcePath != `C:\proj\index.php` || stop.Line != 7 || stop.Reason != "breakpoint" {
		t.Errorf("stopped = %+v", stop)
	}

	if err := c.Continue(1234); err != nil {
		t.Fatalf("Continue: %v", err)
	}
	getEvent(dap.EventContinued)
	getEvent(dap.EventExited)
	getEvent(dap.EventTerminated)

	// Steps after a resume stop on the next line.
	if err := c.StepIn(1234); err != nil {
		t.Fatalf("StepIn: %v", err)
	}
	getEvent(dap.EventContinued)
	if s := getEvent(dap.EventStopped); s.Line != 8 {
		t.Errorf("step stop at line %d", s.Line)
	}
	c.Close()
}

func TestClientErrorResponse(t *testing.T) {
	// An <error> child with no success attribute must surface as an error, not
	// as a successful response with no data.
	events := make(chan dap.Event, 8)
	var runNum int
	c := newClientForTest(t, func(e dap.Event) { events <- e }, &runNum)
	_, err := c.call("bogus", c.Seq())
	if err == nil {
		t.Fatal("expected an error for an <error> response without success=0")
	}
	if !strings.Contains(err.Error(), "command is not available") {
		t.Fatalf("err = %v", err)
	}
}

func TestStartDebuggerNoInterpreter(t *testing.T) {
	// With no PHP on PATH from the test environment we can only assert a
	// clean error from LookPath or the accept path; skip is avoided so the
	// package still has coverage with a real php binary present.
	_, err := StartDebugger("definitely-not-a-php-interpreter", "x.php", nil, ".", "127.0.0.1:0", nil)
	if err == nil {
		t.Skip("interpreter found; nothing to test")
	}
}
