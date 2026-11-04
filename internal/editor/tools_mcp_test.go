package editor

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"dmed/internal/ai"
	"dmed/internal/config"
	"dmed/internal/mcp"
)

// fakeMCPServer is the client end of an in-memory MCP session answering
// tools/call with a canned text. The manager accepts it without a handshake:
// these tests pin the editor's routing and parking, not the protocol (the
// protocol has its own tests in internal/mcp).
func fakeMCPServer(t *testing.T, reply string, isErr bool) *mcp.Client {
	t.Helper()
	a, b := net.Pipe()
	c := mcp.New(a, a, nil)
	go func() {
		scan := bufio.NewScanner(b)
		for scan.Scan() {
			var req struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      json.RawMessage `json:"id"`
				Method  string          `json:"method"`
			}
			if json.Unmarshal(scan.Bytes(), &req) != nil || req.ID == nil {
				continue
			}
			resp := map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(req.ID),
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": reply}},
					"isError": isErr,
				},
			}
			out, _ := json.Marshal(resp)
			b.Write(append(out, '\n'))
		}
	}()
	t.Cleanup(func() {
		_ = c.Close()
		a.Close()
		b.Close()
	})
	return c
}

func mcpHarness(t *testing.T, reply string) Model {
	t.Helper()
	m := Model{root: t.TempDir()}
	cfg := config.Defaults()
	m.cfg = cfg
	m.mcpMgr = mcp.NewManager([]mcp.Result{{
		Name:   "srv",
		Client: fakeMCPServer(t, reply, false),
		Tools: []mcp.Tool{
			{Name: "alpha", Description: "the alpha tool", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "beta", Description: "the beta tool"},
		},
	}})
	return m
}

func TestMCPToolDefsJoinTheChatList(t *testing.T) {
	m := mcpHarness(t, "unused")
	defs := m.activeChatToolDefs()
	var alpha, beta ai.ToolDef
	for _, d := range defs {
		if d.Name == "mcp_srv_alpha" {
			alpha = d
		}
		if d.Name == "mcp_srv_beta" {
			beta = d
		}
	}
	if alpha.Name == "" {
		t.Fatal("the MCP tool is missing from the chat list")
	}
	if alpha.Description != "the alpha tool" || alpha.Parameters == nil {
		t.Fatalf("def = %+v", alpha)
	}
	// A tool without a declared schema still needs a parameters node.
	if beta.Parameters == nil {
		t.Fatal("an empty schema must become a bare object")
	}

	// Plan mode guarantees absence: nothing whose capability the editor
	// cannot verify survives readOnlyToolDefs.
	m.cfg.AI.Mode_ = string(config.ModePlan)
	for _, d := range m.activeChatToolDefs() {
		if strings.HasPrefix(d.Name, "mcp_") {
			t.Fatalf("plan mode must drop MCP tools, got %s", d.Name)
		}
	}
}

func TestMCPToolDefsHonourTheToolLists(t *testing.T) {
	m := mcpHarness(t, "unused")
	m.cfg.AI.ToolsDisabled = []string{"mcp_srv_beta"}
	names := map[string]bool{}
	for _, d := range m.activeChatToolDefs() {
		names[d.Name] = true
	}
	if names["mcp_srv_beta"] {
		t.Error("tools_disabled must reach MCP tools")
	}
	if !names["mcp_srv_alpha"] {
		t.Error("the other MCP tool must survive")
	}
}

func TestExecChatToolMCPAsksByDefault(t *testing.T) {
	m := mcpHarness(t, "hello from srv")
	res := m.execChatTool(ai.ToolCall{Name: "mcp_srv_alpha", Args: `{"q":"x"}`})
	if res.Park.Kind != ParkMCPConfirm {
		t.Fatalf("default mcp_ask must park for confirmation, got %+v", res.Park)
	}
	if res.Park.Server != "srv" || res.Park.Tool != "alpha" || res.Park.Args != `{"q":"x"}` {
		t.Fatalf("park = %+v", res.Park)
	}
	if !strings.Contains(res.Text, "awaiting confirmation") {
		t.Fatalf("placeholder = %q", res.Text)
	}

	// With confirmations off, the same call runs immediately.
	m.cfg.AI.MCPAsk = false
	res = m.execChatTool(ai.ToolCall{Name: "mcp_srv_alpha", Args: `{"q":"x"}`})
	if res.Park.Kind != ParkNone {
		t.Fatalf("mcp_ask = false must not park: %+v", res.Park)
	}
	if !strings.Contains(res.Text, "hello from srv") {
		t.Fatalf("call text = %q", res.Text)
	}
}

func TestMCPUnknownToolFallsThroughToBuiltins(t *testing.T) {
	m := mcpHarness(t, "unused")
	res := m.execChatTool(ai.ToolCall{Name: "READ", Args: `{"arg":"missing.go"}`})
	if !strings.Contains(res.Text, "READ") {
		t.Fatalf("a built-in name must never reach the MCP lookup: %q", res.Text)
	}
	res = m.execChatTool(ai.ToolCall{Name: "mcp_srv_nothere"})
	if !strings.Contains(res.Text, "unknown tool") {
		t.Fatalf("an unexposed mcp_ name must read as unknown: %q", res.Text)
	}
}

func TestMCPConfirmFlowThroughTheParkQueue(t *testing.T) {
	m := chatEditHarness(t, t.TempDir())
	m.mcpMgr = mcp.NewManager([]mcp.Result{{
		Name:   "srv",
		Client: fakeMCPServer(t, "ran it", false),
		Tools:  []mcp.Tool{{Name: "alpha"}},
	}})

	m.handleChatToolsDone("", []ai.ToolCall{
		{ID: "c1", Name: "mcp_srv_alpha", Args: "{}"},
	})
	if m.chatPark == nil || m.chatPark.park.Kind != ParkMCPConfirm {
		t.Fatalf("the MCP call must park, got %+v", m.chatPark)
	}

	// Declining feeds the model the refusal and writes nothing anywhere.
	m.finishChatMCPConfirm(false)
	var sawDecline bool
	for _, res := range m.chatMsgs {
		if strings.Contains(res.Content, "not approved") {
			sawDecline = true
		}
	}
	if !sawDecline {
		t.Fatalf("the model must learn the call was declined: %+v", m.chatMsgs)
	}
}

func TestMCPApproveRunsTheHeldCall(t *testing.T) {
	m := chatEditHarness(t, t.TempDir())
	m.mcpMgr = mcp.NewManager([]mcp.Result{{
		Name:   "srv",
		Client: fakeMCPServer(t, "ran it", false),
		Tools:  []mcp.Tool{{Name: "alpha"}},
	}})
	m.handleChatToolsDone("", []ai.ToolCall{
		{ID: "c1", Name: "mcp_srv_alpha", Args: "{}"},
	})
	if m.chatPark == nil {
		t.Fatal("expected a park")
	}
	m.finishChatMCPConfirm(true)
	var sawRun bool
	for _, res := range m.chatMsgs {
		if strings.Contains(res.Content, "ran it") {
			sawRun = true
		}
	}
	if !sawRun {
		t.Fatalf("the approved call must reach the model: %+v", m.chatMsgs)
	}
}

func TestMCPFingerprintAndStaleGenerations(t *testing.T) {
	m := Model{}
	base := m.mcpFingerprint()
	m.cfg.AI.MCPServers = map[string]string{"b": "x", "a": "y"}
	fp := m.mcpFingerprint()
	if fp == base {
		t.Fatal("adding servers must move the fingerprint")
	}
	// Map iteration is random; the fingerprint must be the same string twice.
	if m.mcpFingerprint() != fp {
		t.Fatal("the fingerprint must not depend on map order")
	}

	// A reply stamped with a superseded generation is dropped.
	m.applyMCPReady(mcpReadyMsg{gen: 41, results: []mcp.Result{{Name: "x", Err: "boom"}}})
	if m.mcpMgr != nil {
		t.Fatal("a stale generation must not install a manager")
	}
	m.applyMCPReady(mcpReadyMsg{gen: m.mcpGen, results: []mcp.Result{{Name: "x", Err: "boom"}}})
	if m.mcpMgr == nil || len(m.mcpMgr.Failures()) != 1 {
		t.Fatalf("the current generation must install: %+v", m.mcpMgr)
	}
	if !strings.Contains(m.mcpStatusLine(), "1 failed") {
		t.Fatalf("status = %q", m.mcpStatusLine())
	}
}

func TestMCPRestartBumpsGenerationAndCloses(t *testing.T) {
	m := mcpHarness(t, "unused")
	m.cfg.AI.MCPServers = map[string]string{"srv": "fake-server"}
	m.mcpSpawn = func(string, string) (*mcp.Client, error) {
		return fakeMCPServer(t, "ran it", false), nil
	}
	oldGen := m.mcpGen
	cmd := m.restartMCP()
	if m.mcpGen != oldGen+1 {
		t.Fatalf("gen = %d, want a bump", m.mcpGen)
	}
	if m.mcpMgr != nil {
		t.Fatal("the old manager must be gone before the new round starts")
	}
	if m.mcpFP == "" {
		t.Fatal("the restart records the new fingerprint")
	}
	if cmd == nil {
		t.Fatal("restart with servers configured must return the spawn round")
	}
	msg := cmd()
	ready, ok := msg.(mcpReadyMsg)
	if !ok || ready.gen != m.mcpGen {
		t.Fatalf("msg = %+v (%T), want the current generation", msg, msg)
	}
	if len(ready.results) != 1 || ready.results[0].Client == nil {
		t.Fatalf("results = %+v, want the fake client", ready.results)
	}
}

func TestMCPCmdIsNilWithoutServers(t *testing.T) {
	m := Model{}
	if m.mcpCmd(1) != nil {
		t.Fatal("no servers configured means no background round at all")
	}
	// An empty command line is a removal, not a server.
	m.cfg.AI.MCPServers = map[string]string{"gone": " "}
	if m.mcpCmd(1) != nil {
		t.Fatal("empty command lines must start nothing")
	}
}

func TestToolBatchTouchesDiskForMCPCalls(t *testing.T) {
	// A server can write anything; the batch must pay the touched-file walk
	// after an MCP call, conservatively.
	if !toolBatchTouchesDisk([]ai.ToolCall{{Name: "mcp_srv_alpha"}}) {
		t.Fatal("an MCP call must count as touching disk")
	}
	if toolBatchTouchesDisk([]ai.ToolCall{{Name: "READ"}}) {
		t.Fatal("a plain read batch must not")
	}
}

func TestMCPManagerCallErrors(t *testing.T) {
	m := mcpHarness(t, "unused")
	// The harness server answers calls; a tool the manager never exposed
	// must report instead of panicking.
	text := m.mcpCallText("srv", "nothere", "{}")
	if !strings.Contains(text, "no longer available") {
		t.Fatalf("text = %q", text)
	}
	m.mcpMgr = nil
	text = m.mcpCallText("srv", "alpha", "{}")
	if !strings.Contains(text, "no longer available") {
		t.Fatalf("text with no manager = %q", text)
	}
	// A dead server reports its death, not a crash.
	dead := mcp.NewManager([]mcp.Result{{Name: "d", Client: deadMCPClient(), Tools: []mcp.Tool{{Name: "t"}}}})
	m.mcpMgr = dead
	text = m.mcpCallText("d", "t", "{}")
	if !strings.Contains(text, "[MCP error]") {
		t.Fatalf("dead server text = %q", text)
	}
}

func deadMCPClient() *mcp.Client {
	a, b := net.Pipe()
	c := mcp.New(a, a, nil)
	a.Close()
	b.Close()
	time.Sleep(20 * time.Millisecond)
	return c
}

func TestMCPStartOneReportsSpawnFailure(t *testing.T) {
	res := mcpStartOne(func(string, string) (*mcp.Client, error) {
		return nil, context.DeadlineExceeded
	}, "test", "srv", "whatever", "")
	if res.Err == "" || res.Client != nil {
		t.Fatalf("res = %+v, want the spawn error reported", res)
	}
}

// TestMCPDefsConvertEmptySchema keeps the conversion honest: the provider
// layer sends Parameters verbatim, so the fallback must be a JSON object even
// when the server sent no schema at all.
func TestMCPDefsConvertEmptySchema(t *testing.T) {
	m := mcpHarness(t, "unused")
	defs := m.mcpToolDefs()
	var beta ai.ToolDef
	for _, d := range defs {
		if d.Name == "mcp_srv_beta" {
			beta = d
		}
	}
	if beta.Parameters == nil {
		t.Fatal("nil schema must not reach the provider")
	}
	b, err := json.Marshal(beta.Parameters)
	if err != nil || !strings.Contains(string(b), `"type":"object"`) {
		t.Fatalf("fallback schema = %s (%v)", b, err)
	}
}
