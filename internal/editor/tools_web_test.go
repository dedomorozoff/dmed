package editor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/ai"
	"dmed/internal/config"
	"dmed/internal/websearch"
)

const stubPage = `<html><body>
<a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdoc%2Fstrings">strings &amp; runes</a>
<a class="result__snippet" href="#">Documentation for the strings package.</a>
</body></html>`

func stubSearchServer(t *testing.T, hits *int) *websearch.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			*hits++
		}
		_, _ = w.Write([]byte(stubPage))
	}))
	t.Cleanup(srv.Close)
	return websearch.NewWithEndpoint(srv.URL)
}

// TestWebSearchIsOptIn pins the rule for the only tool that leaves the
// workspace: it must not exist unless the user asked for it.
func TestWebSearchIsOptIn(t *testing.T) {
	m := New()
	if toolDefNames(m.activeChatToolDefs()) != nil {
		if hasTool(m.activeChatToolDefs(), "WEB_SEARCH") {
			t.Fatal("web search must not be exposed by default")
		}
	}
	m.cfg.AI.WebSearch = true
	if !hasTool(m.activeChatToolDefs(), "WEB_SEARCH") {
		t.Fatal("web search must appear once enabled")
	}

	// Even enabled, the executor refuses while the setting is off — the tool
	// list is a hint, the executor is the guarantee.
	m.cfg.AI.WebSearch = false
	res := m.execChatTool(ai.ToolCall{Name: "WEB_SEARCH", Args: `{"arg":"go"}`})
	if !strings.Contains(res.Text, "blocked") {
		t.Fatalf("disabled web search must be refused: %q", res.Text)
	}
}

func TestWebSearchReturnsResultsAndSpendsBudget(t *testing.T) {
	hits := 0
	m := New()
	m.cfg.AI.WebSearch = true
	m.cfg.AI.WebSearchBudget = 2
	m.webSearchOverride = stubSearchServer(t, &hits)

	res := m.execChatTool(ai.ToolCall{Name: "web_search", Args: `{"arg":"go strings"}`})
	if !strings.Contains(res.Text, "go strings") || !strings.Contains(res.Text, "go.dev") {
		t.Fatalf("result = %q", res.Text)
	}
	if m.webSearchUsed != 1 {
		t.Fatalf("used = %d, want 1", m.webSearchUsed)
	}

	// The alias must reach the same tool.
	res = m.execChatTool(ai.ToolCall{Name: "WEB_SEARCH", Args: `{"arg":"again"}`})
	if strings.Contains(res.Text, "unknown tool") {
		t.Fatalf("WEB_SEARCH not dispatched: %q", res.Text)
	}
	if m.webSearchUsed != 2 {
		t.Fatalf("used = %d, want 2", m.webSearchUsed)
	}

	// Budget exhausted: blocked, and the tool is withdrawn so a small model
	// stops retrying something that always fails.
	res = m.execChatTool(ai.ToolCall{Name: "WEB_SEARCH", Args: `{"arg":"third"}`})
	if !strings.Contains(res.Text, "limit reached") {
		t.Fatalf("over budget must be blocked: %q", res.Text)
	}
	if hits != 2 {
		t.Fatalf("server hit %d times, want 2 — a blocked query must not reach the network", hits)
	}
	if hasTool(m.activeChatToolDefs(), "WEB_SEARCH") {
		t.Fatal("the tool must disappear once the budget is spent")
	}
}

func TestWebSearchSurfacesFailureToTheModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	m := New()
	m.cfg.AI.WebSearch = true
	m.webSearchOverride = websearch.NewWithEndpoint(srv.URL)

	res := m.execChatTool(ai.ToolCall{Name: "WEB_SEARCH", Args: `{"arg":"q"}`})
	if !strings.Contains(res.Text, "lookup failed") {
		t.Fatalf("a failed lookup must be reported, not swallowed: %q", res.Text)
	}
	if m.webSearchUsed != 1 {
		t.Fatal("a failed query still counts against the budget")
	}
}

// TestPlanModeRemovesMutatingTools is the guarantee, not a policy: in plan mode
// the model has nothing to call.
func TestPlanModeRemovesMutatingTools(t *testing.T) {
	m := New()
	m.cfg.AI.WebSearch = true
	m.cfg.AI.Mode_ = string(config.ModePlan)

	names := toolDefNames(m.activeChatToolDefs())
	for _, gone := range []string{"RUN", "EDIT", "REPLACE", "SWITCH_MODE"} {
		if containsStr(strings.Join(names, ","), gone) {
			t.Fatalf("plan mode must not expose %s, got %v", gone, names)
		}
	}
	for _, kept := range []string{"READ", "SEARCH", "LIST_DIR", "GLOB", "TODO_WRITE", "ASK_USER", "WEB_SEARCH"} {
		if !containsStr(strings.Join(names, ","), kept) {
			t.Fatalf("plan mode must keep %s, got %v", kept, names)
		}
	}
	if !strings.Contains(m.modeNotice(), "PLAN MODE") {
		t.Fatalf("the model must be told, notice = %q", m.modeNotice())
	}
	mm := New()
	if notice := mm.modeNotice(); notice != "" {
		t.Fatalf("act mode must not add a mode notice, got %q", notice)
	}

	// Back to act: everything returns, and the notice goes away.
	m.toggleMode()
	if m.cfg.AI.AgentMode() != config.ModeAct {
		t.Fatal("toggleMode must leave plan mode")
	}
	names = toolDefNames(m.activeChatToolDefs())
	if !containsStr(strings.Join(names, ","), "EDIT") {
		t.Fatalf("act mode must expose EDIT, got %v", names)
	}
	if m.modeNotice() != "" {
		t.Fatalf("act mode notice = %q, want empty", m.modeNotice())
	}
}

func TestSwitchModeParksForApproval(t *testing.T) {
	m := chatEditHarness(t, t.TempDir())
	m.cfg.AI.Mode_ = string(config.ModePlan)

	cmd := m.handleChatToolsDone("", []ai.ToolCall{{ID: "c1", Name: "SWITCH_MODE", Args: `{"mode":"act"}`}})
	if cmd != nil {
		t.Fatal("switching modes must park for a human decision")
	}
	if m.chatPark == nil || m.chatPark.park.Kind != ParkSwitchMode {
		t.Fatalf("park = %+v", m.chatPark)
	}
	if m.cfg.AI.AgentMode() != config.ModePlan {
		t.Fatal("the mode must not change before the user agrees")
	}

	m.handleChatParkKey(teaKeyY())

	if m.chatPark != nil {
		t.Fatal("the park must be resolved")
	}
	if m.cfg.AI.AgentMode() != config.ModeAct {
		t.Fatal("y must switch to act mode")
	}
	var told bool
	for _, msg := range m.chatMsgs {
		if msg.Role == "tool" && strings.Contains(msg.Content, "now in act mode") {
			told = true
		}
	}
	if !told {
		t.Fatalf("the model must learn the switch happened: %+v", m.chatMsgs)
	}
}

func TestSwitchModeDeclineKeepsPlanning(t *testing.T) {
	m := chatEditHarness(t, t.TempDir())
	m.cfg.AI.Mode_ = string(config.ModePlan)
	m.handleChatToolsDone("", []ai.ToolCall{{ID: "c1", Name: "SWITCH_MODE", Args: `{"mode":"act"}`}})

	m.handleChatParkKey(teaKeyN())

	if m.cfg.AI.AgentMode() != config.ModePlan {
		t.Fatal("n must keep the model in plan mode")
	}
	var told bool
	for _, msg := range m.chatMsgs {
		if msg.Role == "tool" && strings.Contains(msg.Content, "switch_mode denied") {
			told = true
		}
	}
	if !told {
		t.Fatalf("the model must learn the request was declined: %+v", m.chatMsgs)
	}
}

func TestSwitchModeOnlyWorksInPlanMode(t *testing.T) {
	m := chatEditHarness(t, t.TempDir())
	// Act mode: there is nothing to switch to, and the model must not be able
	// to re-request the same thing forever.
	res := m.execChatTool(ai.ToolCall{Name: "SWITCH_MODE", Args: `{"mode":"act"}`})
	if res.Park.Kind != ParkNone {
		t.Fatal("act mode must not park")
	}
	if !strings.Contains(res.Text, "only available in plan mode") {
		t.Fatalf("result = %q", res.Text)
	}
	res = m.execChatTool(ai.ToolCall{Name: "SWITCH_MODE", Args: `{"mode":"nonsense"}`})
	if !strings.Contains(res.Text, "error") {
		t.Fatalf("a bad mode must error: %q", res.Text)
	}
}

func hasTool(defs []ai.ToolDef, name string) bool {
	for _, d := range defs {
		if d.Name == name {
			return true
		}
	}
	return false
}

// teaKeyY / teaKeyN are the parked-decision keys. They go through the same
// handler the user does, so the tests exercise dispatch rather than the
// resolver alone.
func teaKeyY() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'y', Text: "y"} }
func teaKeyN() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'n', Text: "n"} }
