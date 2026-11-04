package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// MaxExposedName bounds an exposed tool name. OpenAI-compatible providers
// reject function names past 64 characters, so a tool that would not fit is
// reported rather than sent and failed at every round.
const MaxExposedName = 64

// Result is the outcome of starting one configured server: either a live
// client with its tool list, or Err explaining why there is neither.
type Result struct {
	Name   string
	Client *Client
	Info   ServerInfo
	Tools  []Tool
	Err    string
}

// Server is one running MCP server as the manager keeps it.
type Server struct {
	Name   string
	Info   ServerInfo
	Client *Client
	Tools  []Tool
}

// Exposed is one server tool under the flat name the model calls it by.
type Exposed struct {
	Name        string // prefixed: mcp_<server>_<tool>
	Server      string
	Tool        string
	Description string
	Schema      json.RawMessage
}

// Manager holds the running servers and the flat tool index. It is built on
// the UI thread from the spawn results and never touches them afterwards;
// clients answer calls synchronously.
type Manager struct {
	servers map[string]*Server
	byName  map[string]Exposed
	order   []string // exposed names, sorted once for a stable defs list
	failed  []string
}

// NewManager indexes the results of a spawn round. Failed servers are kept
// only as reported names.
func NewManager(results []Result) *Manager {
	mgr := &Manager{servers: map[string]*Server{}, byName: map[string]Exposed{}}
	for _, r := range results {
		if r.Client == nil {
			err := r.Err
			if err == "" {
				err = "did not start" // a panicking round leaves the zero value
			}
			mgr.failed = append(mgr.failed, r.Name+": "+err)
			continue
		}
		s := &Server{Name: r.Name, Info: r.Info, Client: r.Client, Tools: r.Tools}
		mgr.servers[r.Name] = s
		for _, t := range r.Tools {
			ex := Exposed{
				Name:        ExposedName(r.Name, t.Name),
				Server:      r.Name,
				Tool:        t.Name,
				Description: t.Description,
				Schema:      t.InputSchema,
			}
			if len(ex.Name) > MaxExposedName {
				mgr.failed = append(mgr.failed, fmt.Sprintf(
					"%s: tool %q name does not fit (%d > %d chars)", r.Name, t.Name, len(ex.Name), MaxExposedName))
				continue
			}
			if _, dup := mgr.byName[ex.Name]; dup {
				mgr.failed = append(mgr.failed, fmt.Sprintf("%s: tool %q collides with an earlier name", r.Name, t.Name))
				continue
			}
			mgr.byName[ex.Name] = ex
		}
	}
	mgr.order = make([]string, 0, len(mgr.byName))
	for n := range mgr.byName {
		mgr.order = append(mgr.order, n)
	}
	sort.Strings(mgr.order)
	return mgr
}

// ExposedName flattens a server name and a tool name into one call name:
// mcp_<server>_<tool>, every character outside [A-Za-z0-9_-] replaced.
func ExposedName(server, tool string) string {
	return "mcp_" + sanitizeName(server) + "_" + sanitizeName(tool)
}

func sanitizeName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// Lookup finds an exposed tool by its flat name.
func (m *Manager) Lookup(name string) (Exposed, bool) {
	if m == nil {
		return Exposed{}, false
	}
	ex, ok := m.byName[name]
	return ex, ok
}

// Defs returns every exposed tool, in a stable (sorted) order.
func (m *Manager) Defs() []Exposed {
	if m == nil {
		return nil
	}
	out := make([]Exposed, 0, len(m.order))
	for _, n := range m.order {
		out = append(out, m.byName[n])
	}
	return out
}

// Call runs one exposed tool with the model's JSON arguments.
func (m *Manager) Call(ctx context.Context, ex Exposed, argsJSON string) (ToolOutcome, error) {
	s, ok := m.servers[ex.Server]
	if !ok {
		return ToolOutcome{}, fmt.Errorf("mcp: server %q is not running", ex.Server)
	}
	if !s.Client.Alive() {
		return ToolOutcome{}, s.Client.deathErr()
	}
	var args map[string]any
	if strings.TrimSpace(argsJSON) != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return ToolOutcome{}, fmt.Errorf("mcp: bad arguments from the model: %w", err)
		}
	}
	return s.Client.CallTool(ctx, ex.Tool, args)
}

// ToolCount reports how many tools the model can call.
func (m *Manager) ToolCount() int {
	if m == nil {
		return 0
	}
	return len(m.byName)
}

// ServerNames lists the running servers, sorted.
func (m *Manager) ServerNames() []string {
	if m == nil {
		return nil
	}
	out := make([]string, 0, len(m.servers))
	for n := range m.servers {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Failures reports, one line each, the servers that never came up and the
// tools that were dropped.
func (m *Manager) Failures() []string {
	if m == nil {
		return nil
	}
	return m.failed
}

// Close ends every session. Called on quit and before a respawn, so a hot
// reload of [ai] mcp.* never leaves the old processes attached to nothing.
func (m *Manager) Close() {
	if m == nil {
		return
	}
	for _, s := range m.servers {
		_ = s.Client.Close()
	}
}
