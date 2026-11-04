package editor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/agent"
	"dmed/internal/ai"
	"dmed/internal/mcp"
)

func parkHarness(t *testing.T) Model {
	t.Helper()
	m := chatEditHarness(t, t.TempDir())
	m.cfg.AI.AllowRun = "ask"
	return m
}

func TestParkBadgeAppearsOnlyForStacks(t *testing.T) {
	m := parkHarness(t)
	m.handleChatToolsDone("", []ai.ToolCall{
		{ID: "c1", Name: "RUN", Args: `{"arg":"echo one"}`},
	})
	if strings.Contains(m.msg, "[1/1]") {
		t.Fatalf("a single decision needs no ceremony: %q", m.msg)
	}
	if !strings.Contains(m.msg, "RUN: echo one") {
		t.Fatalf("status = %q", m.msg)
	}

	// Two RUNs plus a proposed EDIT (the review counts as one more entry):
	// the badge shows the shared stack depth.
	m2 := parkHarness(t)
	m2.handleChatToolsDone("", []ai.ToolCall{
		{ID: "c1", Name: "RUN", Args: `{"arg":"echo one"}`},
		{ID: "c2", Name: "RUN", Args: `{"arg":"echo two"}`},
		{ID: "c3", Name: "EDIT", Args: `{"path":"x.txt","content":"y"}`},
	})
	if !strings.HasPrefix(m2.msg, "[1/3]") {
		t.Fatalf("badge missing: %q", m2.msg)
	}
}

func TestParkPeekCyclesWithoutMovingTheQueue(t *testing.T) {
	m := parkHarness(t)
	m.handleChatToolsDone("", []ai.ToolCall{
		{ID: "c1", Name: "RUN", Args: `{"arg":"first"}`},
		{ID: "c2", Name: "RUN", Args: `{"arg":"second"}`},
	})
	if !strings.Contains(m.msg, "first") {
		t.Fatalf("head = %q", m.msg)
	}
	m.parkCycle()
	if !strings.Contains(m.msg, "second") || !strings.Contains(m.msg, "viewing") {
		t.Fatalf("peeked line = %q", m.msg)
	}
	// The queue itself did not move: the head is still "first".
	if m.chatPark.park.Text != "first" || len(m.chatParks) != 1 {
		t.Fatalf("peek reordered the queue: %+v", m.chatPark)
	}
	// Cycling wraps back to the head.
	m.parkCycle()
	if !strings.Contains(m.msg, "first") {
		t.Fatalf("wrap = %q", m.msg)
	}

	// Answering while peeked still decides the head, not the view: the y/n
	// arms never read parkPeek.
	m.parkCycle() // peek the second
	m.handleChatParkKey("y", tea.KeyPressMsg{})
	if m.chatPark == nil || m.chatPark.park.Text != "second" {
		t.Fatalf("head was answered while peeking: %+v", m.chatPark)
	}
	if m.parkPeek != 0 {
		t.Fatalf("the new head must start unpeeked, got %d", m.parkPeek)
	}
}

func TestParkPresenterCoversEveryKind(t *testing.T) {
	m := parkHarness(t)
	kinds := []Park{
		{Kind: ParkRunConfirm, Text: "cmd"},
		{Kind: ParkMCPConfirm, Text: "srv/tool"},
		{Kind: ParkSwitchMode},
		{Kind: ParkSubAgent, Text: "t1"},
	}
	for _, p := range kinds {
		if line := m.parkLabel(p); line == "" {
			t.Fatalf("kind %d has no presentation", p.Kind)
		}
	}
	// An MCP park reads through the same hint as RUN: one flow, four kinds.
	run := m.parkLabel(Park{Kind: ParkRunConfirm, Text: "cmd"})
	mcp := m.parkLabel(Park{Kind: ParkMCPConfirm, Text: "srv/tool"})
	if !strings.HasSuffix(run, mcp[strings.Index(mcp, "("):]) {
		t.Fatalf("hints diverge: %q vs %q", run, mcp)
	}
}

func TestParkUIWithMCPAndRunMixed(t *testing.T) {
	m := parkHarness(t)
	m.mcpMgr = mcp.NewManager([]mcp.Result{{
		Name:   "srv",
		Client: fakeMCPServer(t, "unused", false),
		Tools:  []mcp.Tool{{Name: "alpha"}},
	}})
	m.cfg.AI.MCPAsk = true
	m.handleChatToolsDone("", []ai.ToolCall{
		{ID: "c1", Name: "mcp_srv_alpha", Args: "{}"},
		{ID: "c2", Name: "RUN", Args: `{"arg":"ls"}`},
	})
	if !strings.HasPrefix(m.msg, "[1/2]") || !strings.Contains(m.msg, "MCP srv/alpha") {
		t.Fatalf("first park = %q", m.msg)
	}
	m.handleChatParkKey("n", tea.KeyPressMsg{}) // deny the MCP call
	if m.chatPark == nil || m.chatPark.park.Kind != ParkRunConfirm {
		t.Fatalf("the RUN park must follow: %+v", m.chatPark)
	}
	if m.msg == "" || !strings.Contains(m.msg, "RUN: ls") {
		t.Fatalf("second park = %q", m.msg)
	}
}

func TestFinalizeResetsTheStack(t *testing.T) {
	m := parkHarness(t)
	m.chatPark = &toolPark{park: Park{Kind: ParkRunConfirm, Text: "x"}}
	m.chatParks = []toolPark{{park: Park{Kind: ParkRunConfirm, Text: "y"}}}
	m.parkPeek = 1
	m.clearChatParks()
	if m.parkPeek != 0 {
		t.Fatal("peek must not survive a reset into an unrelated conversation")
	}
	if m.parkTotal() != 0 {
		t.Fatal("total must read the cleared queue")
	}
}

func TestParkTotalCountsReviewAsOneEntry(t *testing.T) {
	m := parkHarness(t)
	m.chatPendingChanges = []agent.Change{{Path: "a.go"}}
	if m.parkTotal() != 1 {
		t.Fatalf("a lone review is one decision, got %d", m.parkTotal())
	}
	m.chatPark = &toolPark{park: Park{Kind: ParkRunConfirm, Text: "x"}}
	if m.parkTotal() != 2 {
		t.Fatalf("park + review = %d", m.parkTotal())
	}
}
