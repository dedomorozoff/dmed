package editor

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/agent"
	"dmed/internal/ai"
	"dmed/internal/config"
)

func subagentHarness(t *testing.T) *Model {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.go"), "package a\n")
	m := chatEditHarness(t, dir)
	m.agentQueue = agent.NewQueue(nil)
	return &m
}

// TestSubAgentToolOnlyOfferedWhenItCanRun: a model must not be handed a tool
// whose queue does not exist, or it burns a round to learn that.
func TestSubAgentToolOnlyOfferedWhenItCanRun(t *testing.T) {
	m := New()
	if hasTool(m.activeChatToolDefs(), "SUB_AGENT") {
		t.Fatal("delegation must be absent before the agent queue exists")
	}
	m.agentQueue = agent.NewQueue(nil)
	if !hasTool(m.activeChatToolDefs(), "SUB_AGENT") {
		t.Fatal("delegation must appear once the queue is running")
	}
	// Plan mode: delegation is a mutation of the world, so it is withheld.
	m.cfg.AI.Mode_ = string(config.ModePlan)
	if hasTool(m.activeChatToolDefs(), "SUB_AGENT") {
		t.Fatal("plan mode must not offer delegation")
	}
}

// TestSubAgentToolSetIsReadAndProposeOnly pins the guarantee that a background
// task cannot run shell commands, ask the user, or delegate again.
func TestSubAgentToolSetIsReadAndProposeOnly(t *testing.T) {
	tools := newSubAgentTools(t.TempDir(), config.Defaults())
	names := toolDefNames(tools.Defs())
	joined := strings.Join(names, ",")

	for _, want := range []string{"READ", "SEARCH", "LIST_DIR", "GLOB", "REPLACE", "EDIT"} {
		if !containsStr(joined, want) {
			t.Errorf("a sub-agent needs %s, got %v", want, names)
		}
	}
	for _, gone := range []string{"RUN", "ASK_USER", "SUB_AGENT", "TODO_WRITE", "WEB_SEARCH", "SWITCH_MODE"} {
		if containsStr(joined, gone) {
			t.Errorf("a sub-agent must not get %s, got %v", gone, names)
		}
	}

	// And the executor refuses them even if a model asks anyway.
	for _, name := range []string{"RUN", "ASK_USER", "SUB_AGENT"} {
		text, _ := tools.Exec(name, `{"arg":"rm -rf /"}`)
		if !strings.Contains(text, "not available") {
			t.Errorf("%s = %q, want a refusal", name, text)
		}
	}
}

// TestSubAgentExecUsesTheSameCodeAsChat: the tool implementations are shared
// (toolEnv), so a sub-agent gets the same path confinement as the chat.
func TestSubAgentExecUsesTheSameCodeAsChat(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir+"/a.go", "package a\n")
	cfg := config.Defaults()
	cfg.AI.RestrictToRoot = true
	tools := newSubAgentTools(dir, cfg)

	text, _ := tools.Exec("READ", `{"arg":"a.go"}`)
	if !containsStr(text, "package a") {
		t.Fatalf("READ = %q", text)
	}
	text, _ = tools.Exec("READ", `{"arg":"../../etc/passwd"}`)
	if !containsStr(text, "outside project root") {
		t.Fatalf("a sub-agent must respect restrict_to_root: %q", text)
	}
	text, chg := tools.Exec("EDIT", `{"path":"a.go","content":"package b\n"}`)
	if chg == nil {
		t.Fatalf("EDIT must propose a change, got %q", text)
	}
	if filepath.Clean(chg.Path) != filepath.Clean(filepath.Join(dir, "a.go")) {
		t.Fatalf("change path = %q, want the project file", chg.Path)
	}
}

// TestSubAgentDelegationParksChat: the chat must wait for the task, and the task
// must be a real queue entry the user can watch and review.
func TestSubAgentDelegationParksChat(t *testing.T) {
	m := subagentHarness(t)

	cmd := m.handleChatToolsDone("", []ai.ToolCall{{ID: "c1", Name: "SUB_AGENT",
		Args: `{"task":"rename Widget to Gadget everywhere"}`}})
	if cmd != nil {
		t.Fatal("the chat must park while the sub-agent works")
	}
	if m.chatPark == nil || m.chatPark.park.Kind != ParkSubAgent {
		t.Fatalf("park = %+v", m.chatPark)
	}
	task, ok := m.agentQueue.Find(m.chatPark.park.Text)
	if !ok {
		t.Fatal("the delegated task must exist on the queue")
	}
	if task.Kind != agent.KindSubagent {
		t.Fatalf("kind = %q", task.Kind)
	}
	if !strings.Contains(m.msg, "Esc") {
		t.Fatalf("the wait must be visible and cancellable, status = %q", m.msg)
	}

	// Nothing may be written while the sub-agent runs, and the wait is visible.
	if !m.subAgentRunning() {
		t.Fatal("a queued delegation must count as in flight")
	}
}

// TestSubAgentResultReachesTheModel checks the whole round trip: the queued task
// finishes, the poll resolves the park, and the model is told what happened and
// where the diff waits.
func TestSubAgentResultReachesTheModel(t *testing.T) {
	m := subagentHarness(t)
	m.handleChatToolsDone("", []ai.ToolCall{{ID: "c1", Name: "SUB_AGENT", Args: `{"task":"do it"}`}})
	id := m.chatPark.park.Text

	// Simulate the worker: the sub-agent proposed two changes and finished.
	task, _ := m.agentQueue.Find(id)
	m.agentQueue.SetChanges(id, []agent.Change{
		{Path: task.ID + "-a.go", Orig: "old", New: "new"},
		{Path: task.ID + "-b.go", Orig: "old", New: "new"},
	})
	if m.pollSubAgentPark() {
		t.Fatal("the poll must not resolve while the task is still in review")
	}

	// The reviewer accepts the diff, which is what ends the task.
	m.agentQueue.SetStatus(id, agent.StatusApplied)
	if !m.pollSubAgentPark() {
		t.Fatal("a finished task must release the parked chat")
	}
	if m.chatPark != nil {
		t.Fatal("the park must be cleared")
	}
	var report string
	for _, msg := range m.chatMsgs {
		if msg.Role == "tool" && strings.Contains(msg.Content, "[SUB_AGENT done]") {
			report = msg.Content
		}
	}
	if report == "" {
		t.Fatalf("the model must get a report: %+v", m.chatMsgs)
	}
	if !strings.Contains(report, "2 file change(s)") {
		t.Fatalf("report = %q", report)
	}
	if !strings.Contains(report, "agent panel") {
		t.Fatalf("the report must say where the diff waits, got %q", report)
	}
	if containsStr(report, "awaiting") {
		t.Fatalf("the placeholder must be gone, got %q", report)
	}
}

func TestSubAgentFailureAndCancelReports(t *testing.T) {
	m := subagentHarness(t)

	// Failure.
	m.handleChatToolsDone("", []ai.ToolCall{{ID: "c1", Name: "SUB_AGENT", Args: `{"task":"x"}`}})
	id := m.chatPark.park.Text
	m.agentQueue.SetError(id, "429 rate limited")
	m.pollSubAgentPark()
	if !chatTellsModel(m, "failed") {
		t.Fatal("a failed delegation must be reported to the model")
	}

	// Cancellation through Esc, which is the only way out of a long wait.
	m2 := subagentHarness(t)
	m2.handleChatToolsDone("", []ai.ToolCall{{ID: "c1", Name: "SUB_AGENT", Args: `{"task":"y"}`}})
	id2 := m2.chatPark.park.Text
	m2.handleChatParkKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	task, ok := m2.agentQueue.Find(id2)
	if !ok {
		t.Fatal("the task must still exist after a cancel")
	}
	if task.Status != agent.StatusCancelled {
		t.Fatalf("status = %q, want cancelled", task.Status)
	}
	m2.pollSubAgentPark()
	if !chatTellsModel(m2, "cancelled") {
		t.Fatalf("a cancelled delegation must be reported: %+v", m2.chatMsgs)
	}
}

func TestSubAgentNoChangesIsNotAFailure(t *testing.T) {
	m := subagentHarness(t)
	m.handleChatToolsDone("", []ai.ToolCall{{ID: "c1", Name: "SUB_AGENT", Args: `{"task":"look"}`}})
	id := m.chatPark.park.Text
	m.agentQueue.SetStatus(id, agent.StatusDone)
	m.pollSubAgentPark()

	if !chatTellsModel(m, "proposed no changes") {
		t.Fatalf("the model must learn nothing was proposed: %+v", m.chatMsgs)
	}
	if containsStr(strings.Join(chatToolTexts(m), " "), "failed") {
		t.Fatal("finishing without changes is not a failure")
	}
}

func TestSubAgentRejectsEmptyTask(t *testing.T) {
	m := subagentHarness(t)
	res := m.execChatTool(ai.ToolCall{Name: "SUB_AGENT", Args: `{"task":"  "}`})
	if !strings.Contains(res.Text, "empty task") {
		t.Fatalf("result = %q", res.Text)
	}
	if res.Park.Kind != ParkNone {
		t.Fatal("an empty task must not park")
	}
}

// chatTellsModel reports whether the tool results in the transcript contain text.
func chatTellsModel(m *Model, want string) bool {
	return containsStr(strings.Join(chatToolTexts(m), "\n"), want)
}

func chatToolTexts(m *Model) []string {
	var out []string
	for _, msg := range m.chatMsgs {
		if msg.Role == "tool" {
			out = append(out, msg.Content)
		}
	}
	return out
}
