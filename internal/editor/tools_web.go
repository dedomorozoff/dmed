package editor

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/config"
	"dmed/internal/websearch"
)

// The two tools that live outside the filesystem: WEB_SEARCH, the only request
// that leaves the machine, and SWITCH_MODE, the only one that changes how the
// model is allowed to behave. Both are opt-in and both are counted.

// webSearchDef (in tools.go) is appended to the tool list only when enabled.

// execWebSearch runs one query. Three things must hold even if the model gets
// stuck in a loop: the feature has to be on, the session has to have budget
// left, and the result has to say plainly that the request left the workspace.
func (m *Model) execWebSearch(query string) ToolResult {
	if !m.cfg.AI.WebSearch {
		return ToolResult{Text: "[web_search blocked] web access is off (set web_search = true)"}
	}
	if m.webBudgetSpent() {
		return ToolResult{Text: "[web_search blocked] session query limit reached (" +
			fmt.Sprint(m.webSearchBudget()) + ")"}
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return ToolResult{Text: "[web_search error] empty query"}
	}
	m.webSearchUsed++

	client := m.webClient()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	results, err := client.Search(ctx, query)
	if err != nil {
		// A failed lookup is an answer the model can reason about, not a
		// reason to abandon the task.
		return ToolResult{Text: "[web_search " + query + "] lookup failed: " + err.Error()}
	}
	return ToolResult{Text: websearch.Format(query, results)}
}

// webSearchBudget is the per-session query cap; a missing setting falls back to
// the shipped default so an old config still has a bound.
func (m *Model) webSearchBudget() int {
	if m.cfg.AI.WebSearchBudget > 0 {
		return m.cfg.AI.WebSearchBudget
	}
	return config.Defaults().AI.WebSearchBudget
}

// webBudgetSpent reports whether the session has used up its web queries. Once
// it is, the tool is withdrawn from the model's list as well as blocked at
// execution, so a small model does not keep trying something that always fails.
func (m *Model) webBudgetSpent() bool {
	return m.webSearchUsed >= m.webSearchBudget()
}

// webClient returns the search client, building it on first use. Tests point
// m.webSearchOverride at a local server, so no unit test touches the network.
func (m *Model) webClient() *websearch.Client {
	if m.webSearchOverride != nil {
		return m.webSearchOverride
	}
	if m.web == nil {
		m.web = websearch.New()
	}
	return m.web
}

// ---- plan / act modes ----

// toggleMode switches the session between planning and acting. Only a human may
// leave plan mode: that is the whole point of the mode.
func (m *Model) toggleMode() {
	if m.cfg.AI.AgentMode() == config.ModePlan {
		m.cfg.AI.Mode_ = string(config.ModeAct)
		m.msg = m.t("chat.mode_act")
	} else {
		m.cfg.AI.Mode_ = string(config.ModePlan)
		m.msg = m.t("chat.mode_plan")
	}
	if m.chatOpen {
		m.rebuildChatRows()
	}
}

// execSwitchMode lets the model ask to leave plan mode — never to enter it, and
// never without the human agreeing. The question parks the loop like any other
// decision, so there is exactly one approval mechanism to understand.
func (m *Model) execSwitchMode(mode string) ToolResult {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case string(config.ModeAct), string(config.ModePlan):
	default:
		return ToolResult{Text: "[switch_mode error] mode must be \"act\" or \"plan\""}
	}
	if m.cfg.AI.AgentMode() != config.ModePlan {
		return ToolResult{Text: "[switch_mode] only available in plan mode (current: " +
			string(m.cfg.AI.AgentMode()) + ")"}
	}
	if mode == string(config.ModePlan) {
		return ToolResult{Text: "[switch_mode] already in plan mode"}
	}
	return ToolResult{
		Text: "[switch_mode] waiting for approval to leave plan mode",
		Park: Park{Kind: ParkSwitchMode, Text: string(config.ModeAct)},
	}
}

// finishChatSwitchMode resolves the parked switch: true switches to act, false
// leaves the model in plan and tells it the request was declined, so it can keep
// planning instead of retrying blindly.
func (m *Model) finishChatSwitchMode(approved bool) tea.Cmd {
	park := m.chatPark
	m.chatPark = nil
	m.msg = ""
	if approved {
		m.cfg.AI.Mode_ = string(config.ModeAct)
		if park != nil {
			resolveParkedResult(m.chatPendingResults, park.resultIdx,
				"[switch_mode] now in act mode: RUN, EDIT and REPLACE are available, changes still need your review")
		}
	} else if park != nil {
		resolveParkedResult(m.chatPendingResults, park.resultIdx,
			"[switch_mode denied] stay in plan mode: keep investigating and updating the plan with TODO_WRITE")
	}
	m.rebuildChatRows()
	if m.startNextPark() {
		return nil
	}
	return m.finalizeChatTools()
}

// modeNotice is the system line that tells the model which mode it is in and
// what it may do. It is refreshed on every turn so a mode change is never
// something the model has to remember from earlier in the conversation.
func (m *Model) modeNotice() string {
	if m.cfg.AI.AgentMode() == config.ModePlan {
		return "PLAN MODE: read-only. You have READ, SEARCH, LIST_DIR, GLOB, the plan tools and ASK_USER. " +
			"You cannot edit or run anything. Finish your plan with TODO_WRITE, then call SWITCH_MODE(\"act\") " +
			"to ask the user for permission to apply it."
	}
	return ""
}

// planSystemPrompt is appended to the system message in plan mode; kept next to
// modeNotice so the two halves of the same rule live together.
