package editor

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/ai"
	"dmed/internal/debug"
	"dmed/internal/mcp"
)

// MCP tool servers ([ai] mcp.<name> = command) join the chat tool loop: their
// tools appear beside READ and SEARCH under a flat mcp_<server>_<tool> name,
// and every call is confirmed by the human before the server runs it — the
// same park a RUN command raises under allow_run = ask, because a server
// executes code the editor cannot inspect.
//
// The session itself is asynchronous like the others: spawning N processes
// and shaking hands with each one must not stall the first frame, so the
// whole round runs in a tea.Cmd and lands as mcpReadyMsg stamped with the
// generation it was started for. A .dmed.conf reload that moves the mcp.*
// set bumps the generation, closes the old manager and starts a fresh round;
// a late reply from the superseded set is dropped.

const (
	// mcpInitTimeout bounds the spawn plus handshake plus tools/list round.
	mcpInitTimeout = 20 * time.Second
	// mcpCallTimeout bounds one tool call; the loop waits on the UI thread,
	// like a RUN command, so a wedged server must not wedge the editor.
	mcpCallTimeout = 60 * time.Second
)

// mcpReadyMsg carries one spawn round's results back to the UI thread.
type mcpReadyMsg struct {
	gen     int
	results []mcp.Result
}

// mcpFingerprint is the configured server set in a comparable string: a
// reload respawns only when a server was added, removed or re-pointed.
func (m *Model) mcpFingerprint() string {
	names := make([]string, 0, len(m.cfg.AI.MCPServers))
	for n := range m.cfg.AI.MCPServers {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n)
		b.WriteByte('=')
		b.WriteString(m.cfg.AI.MCPServers[n])
		b.WriteByte(0)
	}
	return b.String()
}

// mcpCmd builds the background round that starts every configured server. An
// empty command line is a deliberate removal (the [lsp] lang.<id> = shape),
// so it starts nothing and reports nothing. mcpSpawn is the test seam: unit
// tests hand in a client over an in-memory pipe instead of spawning a process.
func (m Model) mcpCmd(gen int) tea.Cmd {
	type spec struct{ name, cmdline string }
	var specs []spec
	names := make([]string, 0, len(m.cfg.AI.MCPServers))
	for n := range m.cfg.AI.MCPServers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if cmd := strings.TrimSpace(m.cfg.AI.MCPServers[n]); cmd != "" {
			specs = append(specs, spec{n, cmd})
		}
	}
	if len(specs) == 0 {
		return nil
	}
	spawn := m.mcpSpawn
	if spawn == nil {
		spawn = mcp.Spawn
	}
	version := m.Version
	if version == "" {
		version = "dev"
	}
	root := m.root
	return func() tea.Msg {
		results := make([]mcp.Result, len(specs))
		var wg sync.WaitGroup
		for i, sp := range specs {
			wg.Add(1)
			go func(i int, sp spec) {
				defer wg.Done()
				debug.CapturePanicReport(func() {
					results[i] = mcpStartOne(spawn, version, sp.name, sp.cmdline, root)
				})
			}(i, sp)
		}
		wg.Wait()
		return mcpReadyMsg{gen: gen, results: results}
	}
}

// mcpStartOne runs one server's bring-up: spawn, initialize, tools/list. A
// failure at any step closes the process and reports the reason; a half-start
// server is a leaked process otherwise.
func mcpStartOne(spawn func(string, string) (*mcp.Client, error), version, name, cmdline, dir string) mcp.Result {
	res := mcp.Result{Name: name}
	c, err := spawn(cmdline, dir)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	ctx, cancel := context.WithTimeout(context.Background(), mcpInitTimeout)
	defer cancel()
	if res.Info, err = c.Initialize(ctx, "dmed", version); err == nil {
		if res.Tools, err = c.ListTools(ctx); err == nil {
			res.Client = c
			return res
		}
	}
	_ = c.Close()
	if err != nil {
		res.Err = err.Error()
	} else {
		res.Err = "did not start"
	}
	return res
}

// applyMCPReady installs one spawn round's manager, dropping the results of a
// superseded generation. It returns whether the message was consumed.
func (m *Model) applyMCPReady(msg mcpReadyMsg) {
	if msg.gen != m.mcpGen {
		return
	}
	m.mcpMgr = mcp.NewManager(msg.results)
	if s := m.mcpStatusLine(); s != "" {
		m.msg = s
	}
}

// mcpStatusLine reports the round on the status bar: what came up, and every
// failure by name. A silent partial start is the failure mode nobody notices.
func (m *Model) mcpStatusLine() string {
	if m.mcpMgr == nil {
		return ""
	}
	servers, tools := len(m.mcpMgr.ServerNames()), m.mcpMgr.ToolCount()
	failed := m.mcpMgr.Failures()
	if servers == 0 && tools == 0 && len(failed) == 0 {
		return ""
	}
	s := fmt.Sprintf("mcp: %d server(s), %d tool(s)", servers, tools)
	if len(failed) > 0 {
		s += fmt.Sprintf(", %d failed: %s", len(failed), strings.Join(failed, "; "))
	}
	return s
}

// restartMCP respawns the servers after a config reload moved the set. The
// old processes die on the UI thread (a kill is instant); the new handshake
// round runs in the returned command.
func (m *Model) restartMCP() tea.Cmd {
	m.mcpMgr.Close()
	m.mcpMgr = nil
	bumpGen(&m.mcpGen)
	m.mcpFP = m.mcpFingerprint()
	return m.mcpCmd(m.mcpGen)
}

// mcpToolDefs converts the exposed MCP tools into provider tool definitions.
// An empty inputSchema becomes a bare object schema: providers require a
// parameters node even for a no-argument tool. The [ai] tools_enabled /
// tools_disabled lists curate these names exactly like the built-in ones.
func (m *Model) mcpToolDefs() []ai.ToolDef {
	exposed := m.mcpMgr.Defs()
	if len(exposed) == 0 {
		return nil
	}
	defs := make([]ai.ToolDef, 0, len(exposed))
	for _, t := range exposed {
		schema := any(t.Schema)
		if len(t.Schema) == 0 {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		defs = append(defs, ai.ToolDef{Name: t.Name, Description: t.Description, Parameters: schema})
	}
	return filterToolDefs(defs, m.cfg.AI.ToolsEnabled, m.cfg.AI.ToolsDisabled)
}

// execMCPTool answers one MCP call: parked for a human y/n while mcp_ask is
// on (the default), run directly when the user switched the confirmations off.
func (m *Model) execMCPTool(ex mcp.Exposed, argsJSON string) ToolResult {
	label := ex.Server + "/" + ex.Tool
	if m.cfg.AI.MCPAsk {
		return ToolResult{
			Text: "[MCP awaiting confirmation: " + label + " " + capCommandOutput(argsJSON) + "]",
			Park: Park{Kind: ParkMCPConfirm, Text: label, Server: ex.Server, Tool: ex.Tool, Args: argsJSON},
		}
	}
	return ToolResult{Text: m.mcpCallText(ex.Server, ex.Tool, argsJSON)}
}

// mcpCallText runs the call and flattens its outcome to the transcript text.
// Everything is an error string here rather than a raised error: the model
// must read why a tool did not answer, exactly as with RUN.
func (m *Model) mcpCallText(server, tool, argsJSON string) string {
	ex, ok := m.mcpMgr.Lookup(mcp.ExposedName(server, tool))
	if !ok {
		return "[MCP error] " + server + "/" + tool + " is no longer available (the server was reloaded or never started)"
	}
	ctx, cancel := context.WithTimeout(context.Background(), mcpCallTimeout)
	defer cancel()
	out, err := m.mcpMgr.Call(ctx, ex, argsJSON)
	if err != nil {
		return "[MCP error] " + err.Error()
	}
	text := capCommandOutput(out.Text)
	if out.IsError {
		if text == "" {
			text = "(no message)"
		}
		return "[MCP tool error] " + text
	}
	if text == "" {
		text = "(no output)"
	}
	return "[MCP " + server + "/" + tool + "]\n" + text
}

// finishChatMCPConfirm resolves a parked MCP confirmation: y runs the held
// call, n records the refusal. The placeholder in the pending results is
// replaced with the real outcome, then the loop continues.
func (m *Model) finishChatMCPConfirm(run bool) tea.Cmd {
	park := m.chatPark
	m.chatPark = nil
	if park == nil {
		return m.finalizeChatTools()
	}
	var out string
	if run {
		out = m.mcpCallText(park.park.Server, park.park.Tool, park.park.Args)
	} else {
		out = "[MCP not approved by user] " + park.park.Text
	}
	resolveParkedResult(m.chatPendingResults, park.resultIdx, out)
	m.msg = ""
	m.rebuildChatRows()
	if m.startNextPark() {
		return nil
	}
	return m.finalizeChatTools()
}

// mcpLookup finds an MCP tool by the flat name the model used, before the
// built-in switch sees it. Only mcp-prefixed names are consulted: a plain
// READ always stays the editor's own.
func (m *Model) mcpLookup(name string) (mcp.Exposed, bool) {
	if !strings.HasPrefix(name, "mcp_") {
		return mcp.Exposed{}, false
	}
	return m.mcpMgr.Lookup(name)
}
