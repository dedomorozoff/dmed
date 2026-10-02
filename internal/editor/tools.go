package editor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"dmed/internal/agent"
	"dmed/internal/ai"
	"dmed/internal/ask"
	"dmed/internal/config"
)

// aiRequestOptions translates the configured AI generation parameters into the
// provider-agnostic Options handed to every request (chat, ghost, inline, agent).
func (m *Model) aiRequestOptions() ai.Options {
	return ai.Options{
		Temperature: m.cfg.AI.Temperature,
		NumCtx:      m.cfg.AI.NumCtx,
		NumPredict:  m.cfg.AI.NumPredict,
	}
}

// aiProvider returns the shared AI provider, (re)building it from the current
// config. This is the single place that turns config into a provider: chat,
// inline rewrite, ghost text and the background agent runner all go through
// here, so a new [ai] setting cannot be forgotten in one of the call sites.
//
// The cache is keyed on the settings that actually shape the provider. When
// .dmed.conf is hot-reloaded (or the wizard saves a new provider), the key
// changes and the next call transparently builds a new provider instead of
// keeping the stale one alive for the rest of the session.
//
// model overrides the configured model when non-empty (the chat auto-picks
// the first model the server reports).
func (m *Model) aiProvider(model string) ai.Provider {
	if model == "" {
		// A caller that has not resolved a model yet means "the configured
		// one": an empty model in a request is a guaranteed "Model not found"
		// on providers that require an explicit name (Pollinations 404s).
		model = m.cfg.AI.Model
	}
	if m.aiFallback {
		// The session is running on the keyless fallback (see
		// tryFreeFallback): the user never configured anything and the
		// configured provider did not answer.
		p := config.PollinationsPreset()
		m.ai = ai.NewProvider(ai.Config{
			Type:       ai.OpenAIProvider,
			URL:        m.aiFreeURL(p.BaseURL),
			APIPath:    p.APIPath,
			ModelsPath: p.ModelsPath,
			Model:      model,
		})
		return m.ai
	}
	// The wire kind comes from the preset the label resolves to, not from the
	// label itself: "Pollinations (free, no key)" is not a ProviderType, and
	// any kind NewProvider does not recognise silently becomes an Ollama
	// client — which is how a saved Pollinations setup kept dialing localhost.
	kind := ai.ProviderType(m.currentProviderKind())
	key := strings.Join([]string{
		string(kind), m.cfg.AI.OllamaURL, model, m.cfg.AI.APIKey,
		m.cfg.AI.APIPath, m.cfg.AI.ModelsPath, fmt.Sprint(m.aiFallback),
	}, "\x00")
	if m.ai != nil && m.aiKey == key {
		return m.ai
	}
	m.ai = ai.NewProvider(ai.Config{
		Type:       kind,
		URL:        m.cfg.AI.OllamaURL,
		Model:      model,
		APIKey:     m.cfg.AI.APIKey,
		APIPath:    m.cfg.AI.APIPath,
		ModelsPath: m.cfg.AI.ModelsPath,
	})
	m.aiKey = key
	return m.ai
}

// tryFreeFallback switches the session to a keyless public provider when the
// user never configured anything and the configured provider is unreachable, so
// a fresh install has a working AI instead of a dead panel.
//
// It is deliberately narrow and loud: it fires only for a genuinely untouched
// configuration (config.AIConfig.Unconfigured), only once, and it states in the
// chat that prompts and code now leave the machine, with the config key to turn
// it off. Anything a user chose is never overridden.
func (m *Model) tryFreeFallback() bool {
	if m.aiFallback || !m.cfg.AI.FreeFallback || !m.cfg.AI.Unconfigured() {
		return false
	}
	p := config.PollinationsPreset()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	prov := ai.NewProvider(ai.Config{
		Type: ai.OpenAIProvider, URL: m.aiFreeURL(p.BaseURL),
		APIPath: p.APIPath, ModelsPath: p.ModelsPath,
	})
	if _, err := prov.Models(ctx); err != nil {
		return false
	}
	m.aiFallback = true
	m.chatModel = p.Model
	m.ai = m.aiProvider(p.Model)
	m.chatNotice = m.t("chat.fallback_notice")
	return true
}

// aiFreeURL returns the endpoint to use for the keyless fallback: the preset's
// address, unless a test pointed it at a local stand-in. Tests drive the whole
// fallback through this hook instead of reaching the public service.
func (m *Model) aiFreeURL(pollinationsURL string) string {
	if m.aiFreeURLOverride != "" {
		return m.aiFreeURLOverride
	}
	return pollinationsURL
}

// ParkKind identifies why the chat tool loop is waiting for a human decision.
// A parked tool call has already produced its (placeholder) result text, and
// the loop resumes only after the decision is fed back into that result.
type ParkKind int

const (
	ParkNone ParkKind = iota
	// ParkRunConfirm holds a shell command offered by RUN while allow_run = ask.
	ParkRunConfirm
	// ParkAskUser holds a question from ASK_USER until the user answers.
	ParkAskUser
	// ParkSwitchMode holds a SWITCH_MODE request waiting for a human yes.
	ParkSwitchMode
	// ParkSubAgent holds a delegation until the background sub-agent finishes.
	// The sub-agent's changes are reviewed in the agent panel.
	ParkSubAgent
)

// Park is one pending human decision attached to a tool call.
type Park struct {
	Kind ParkKind
	Text string       // held command (ParkRunConfirm)
	Ask  *ask.Request // question awaiting an answer (ParkAskUser)
}

// ToolResult is what one tool call produced: the text fed back to the model,
// an optional proposed change awaiting diff review, and an optional park that
// suspends the loop until a human answers.
//
// A parked result carries placeholder text; the text is replaced with the real
// outcome once the decision is made (see resolveParkedResult).
type ToolResult struct {
	Text   string
	Change *agent.Change
	Park   Park
}

// resolveParkedResult replaces the placeholder of the parked tool result with
// the outcome the human chose, so the model sees exactly what happened instead
// of an unresolved marker.
func resolveParkedResult(results []ai.Message, idx int, text string) {
	if idx < 0 || idx >= len(results) {
		return
	}
	results[idx].Content = text
}

// coreToolDefs returns the filesystem tools: read, search, run and the two
// that propose edits (which then require human review).
func coreToolDefs() []ai.ToolDef {
	str := func(name, desc string) ai.ToolDef {
		return ai.ToolDef{
			Name:        name,
			Description: desc,
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"arg": map[string]any{"type": "string", "description": "argument"},
				},
				"required": []string{"arg"},
			},
		}
	}
	return []ai.ToolDef{
		str("READ", "Read the full content of a file. arg is the file path."),
		{
			Name:        "SEARCH",
			Description: "Find matching lines in the project. arg is the search query. Returns path:line:content. Set regex:true to treat arg as a regular expression.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"arg":   map[string]any{"type": "string", "description": "search query or regular expression"},
					"regex": map[string]any{"type": "boolean", "description": "if true, treat arg as a regular expression (RE2 syntax)"},
				},
				"required": []string{"arg"},
			},
		},
		str("RUN", "Execute a shell command in the project root and return its output. arg is the command."),
		str("LIST_DIR", "List the entries of one directory: name, kind (dir or file) and size. arg is the directory path; omit or pass . for the project root."),
		{
			Name:        "GLOB",
			Description: "Find files by path pattern, e.g. **/*.go or internal/*/test_*.go. arg is the pattern, relative to the project root. Returns matching paths.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"arg": map[string]any{"type": "string", "description": "glob pattern, may use ** and *"},
				},
				"required": []string{"arg"},
			},
		},
		{
			Name:        "REPLACE",
			Description: "Replace a small, unique search block inside a file with new text. Use this for surgical edits instead of rewriting a whole file. Call this only after READING the file.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    map[string]any{"type": "string", "description": "relative or absolute path to the file"},
					"search":  map[string]any{"type": "string", "description": "exact existing text block to replace (must match the file)"},
					"replace": map[string]any{"type": "string", "description": "the new text to put in place of search"},
				},
				"required": []string{"path", "search", "replace"},
			},
		},
		{
			Name:        "EDIT",
			Description: "Replace the ENTIRE content of a file. Use for new files or large rewrites; prefer REPLACE for small surgical edits. Call this only after READING the file.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    map[string]any{"type": "string", "description": "relative or absolute path to the file"},
					"content": map[string]any{"type": "string", "description": "complete new file content"},
				},
				"required": []string{"path", "content"},
			},
		},
	}
}

// chatToolDefs returns the native function definitions exposed to the chat
// model: the filesystem tools plus the plan/question tools. The model calls
// these via structured JSON arguments rather than emitting fragile text
// markers, so small local models reliably invoke them.
func chatToolDefs() []ai.ToolDef {
	return append(coreToolDefs(), todoDefs()...)
}

// legacyToolName maps the alternative spellings models reach for (they are
// common in system prompts and in other agents' documentation) onto the tools
// dmed actually ships. Unknown names pass through unchanged so the switch can
// report them as unknown.
var legacyToolName = map[string]string{
	"read_file":   "READ",
	"read":        "READ",
	"write_file":  "EDIT",
	"edit_file":   "REPLACE",
	"str_replace": "REPLACE",
	"write":       "EDIT",
	"grep":        "SEARCH",
	"search":      "SEARCH",
	"run_command": "RUN",
	"run":         "RUN",
	"ls":          "LIST_DIR",
	"list":        "LIST_DIR",
	"list_dir":    "LIST_DIR",
	"glob":        "GLOB",
	"todo":        "TODO_WRITE",
	"todo_write":  "TODO_WRITE",
	"todo_set":    "TODO_SET",
	"todo_read":   "TODO_READ",
	"ask_user":    "ASK_USER",
	"askuser":     "ASK_USER",
	"web_search":  "WEB_SEARCH",
	"search_web":  "WEB_SEARCH",
	"websearch":   "WEB_SEARCH",
	"web":         "WEB_SEARCH",
	"switch_mode": "SWITCH_MODE",
	"subagent":    "SUB_AGENT",
	"sub_agent":   "SUB_AGENT",
	"delegate":    "SUB_AGENT",
}

// normalizeToolName resolves a model-provided tool name to a shipped one.
func normalizeToolName(name string) string {
	if t, ok := legacyToolName[strings.ToLower(name)]; ok {
		return t
	}
	return name
}

// filterToolDefs applies the [ai] tools_enabled whitelist (when non-empty) and
// then the tools_disabled blacklist. Matching is case-insensitive and happens
// after name normalization, so `read_file` or `READ` in tools_enabled both
// resolve to READ.
func filterToolDefs(defs []ai.ToolDef, enabled, disabled []string) []ai.ToolDef {
	if len(enabled) == 0 && len(disabled) == 0 {
		return defs
	}
	want := make(map[string]bool, len(enabled))
	for _, n := range enabled {
		want[toolKey(n)] = true
	}
	skip := make(map[string]bool, len(disabled))
	for _, n := range disabled {
		skip[toolKey(n)] = true
	}
	out := make([]ai.ToolDef, 0, len(defs))
	for _, d := range defs {
		key := toolKey(d.Name)
		if len(want) > 0 && !want[key] {
			continue
		}
		if skip[key] {
			continue
		}
		out = append(out, d)
	}
	return out
}

// toolKey is the comparison key for tool names in configuration: normalized and
// case-folded, so "read_file", "READ" and "read" are the same tool.
func toolKey(name string) string {
	return strings.ToUpper(normalizeToolName(name))
}

// webSearchTool is only present when the user turned web access on, so the
// model never sees a tool that reaches outside the workspace by accident.
func webSearchDef() ai.ToolDef {
	return ai.ToolDef{
		Name: "WEB_SEARCH",
		Description: "Search the public web (DuckDuckGo) and read the top results. This is the only tool " +
			"that leaves this machine: use it for facts you cannot read from the project " +
			"(library documentation, error messages, recent releases). " +
			"Queries are limited per session. arg is the search query.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"arg": map[string]any{"type": "string", "description": "search query"},
			},
			"required": []string{"arg"},
		},
	}
}

// activeChatToolDefs returns the tool definitions for the next turn, honouring
// the user's tools_enabled / tools_disabled configuration and the agent mode.
func (m *Model) activeChatToolDefs() []ai.ToolDef {
	defs := chatToolDefs()
	defs = filterToolDefs(defs, m.cfg.AI.ToolsEnabled, m.cfg.AI.ToolsDisabled)
	if m.cfg.AI.WebSearch && !m.webBudgetSpent() {
		defs = append(defs, webSearchDef())
	}
	if m.canDelegate() {
		defs = append(defs, subAgentDef())
	}
	if m.cfg.AI.AgentMode() == config.ModePlan {
		defs = readOnlyToolDefs(defs)
	}
	return defs
}

// canDelegate reports whether a sub-agent could actually run right now. The
// tool is only offered when it would work: a model that calls SUB_AGENT with no
// agent queue just burns a round and gets an error.
func (m *Model) canDelegate() bool {
	return subAgentDepth > 0 && m.agentQueue != nil
}

// readOnlyToolDefs removes everything that can change the machine. In plan mode
// this is not a runtime check but an absent tool: there is nothing for the
// model to call, which is a guarantee rather than a policy.
func readOnlyToolDefs(defs []ai.ToolDef) []ai.ToolDef {
	mutating := map[string]bool{
		"RUN": true, "EDIT": true, "REPLACE": true, "SWITCH_MODE": true, "SUB_AGENT": true,
	}
	out := make([]ai.ToolDef, 0, len(defs))
	for _, d := range defs {
		if mutating[toolKey(d.Name)] {
			continue
		}
		out = append(out, d)
	}
	return out
}

// execChatTool executes one native tool call. The Text of the result is fed
// back to the model; EDIT/REPLACE additionally propose a Change that awaits
// human diff review before it is applied, and RUN may park the loop when
// allow_run = ask. Those results are short summaries — the full proposed
// content lives in the Change so the chat stays compact while the model still
// learns what was proposed.
func (m *Model) execChatTool(tc ai.ToolCall) ToolResult {
	name := normalizeToolName(tc.Name)
	switch name {
	case "TODO_READ", "TODO_WRITE", "TODO_SET":
		return m.execTodoTool(name, tc.Args)
	case "WEB_SEARCH":
		var a struct {
			Arg string `json:"arg"`
		}
		_ = json.Unmarshal([]byte(tc.Args), &a)
		return m.execWebSearch(a.Arg)
	case "SUB_AGENT":
		var a struct {
			Task string `json:"task"`
		}
		_ = json.Unmarshal([]byte(tc.Args), &a)
		return m.execSubAgent(a.Task)
	case "SWITCH_MODE":
		var a struct {
			Mode string `json:"mode"`
		}
		_ = json.Unmarshal([]byte(tc.Args), &a)
		return m.execSwitchMode(a.Mode)
	case "ASK_USER":
		var a struct {
			Question string   `json:"question"`
			Choices  []string `json:"choices"`
		}
		if err := json.Unmarshal([]byte(tc.Args), &a); err != nil {
			return ToolResult{Text: "[ASK_USER error] " + err.Error()}
		}
		return m.execAskUser(a.Question, a.Choices)
	case "READ":
		var a struct {
			Arg string `json:"arg"`
		}
		_ = json.Unmarshal([]byte(tc.Args), &a)
		return ToolResult{Text: m.toolEnv().read(a.Arg)}
	case "SEARCH":
		var a struct {
			Arg   string `json:"arg"`
			Regex bool   `json:"regex"`
		}
		_ = json.Unmarshal([]byte(tc.Args), &a)
		return ToolResult{Text: m.toolEnv().search(a.Arg, a.Regex)}
	case "RUN":
		var a struct {
			Arg string `json:"arg"`
		}
		_ = json.Unmarshal([]byte(tc.Args), &a)
		if strings.EqualFold(m.cfg.AI.AllowRun, "never") {
			return ToolResult{Text: "[RUN blocked] shell execution is disabled (allow_run = never)"}
		}
		if strings.EqualFold(m.cfg.AI.AllowRun, "ask") {
			// Pause for human confirmation of this specific command instead of
			// running it. The chat loop parks and the user picks y/n; see
			// handleChatRunConfirm.
			return ToolResult{
				Text: runConfirmPlaceholder(a.Arg),
				Park: Park{Kind: ParkRunConfirm, Text: a.Arg},
			}
		}
		return ToolResult{Text: runCommand(m.root, a.Arg)}
	case "LIST_DIR":
		var a struct {
			Arg string `json:"arg"`
		}
		_ = json.Unmarshal([]byte(tc.Args), &a)
		return ToolResult{Text: m.chatListDir(a.Arg)}
	case "GLOB":
		var a struct {
			Arg string `json:"arg"`
		}
		_ = json.Unmarshal([]byte(tc.Args), &a)
		return ToolResult{Text: m.chatGlob(a.Arg)}
	case "REPLACE":
		var a struct {
			Path    string `json:"path"`
			Search  string `json:"search"`
			Replace string `json:"replace"`
		}
		if err := json.Unmarshal([]byte(tc.Args), &a); err != nil {
			return ToolResult{Text: "[REPLACE error] " + err.Error()}
		}
		text, chg := m.toolEnv().replace(a.Path, a.Search, a.Replace)
		return ToolResult{Text: text, Change: chg}
	case "EDIT":
		var a struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal([]byte(tc.Args), &a); err != nil {
			return ToolResult{Text: "[EDIT error] " + err.Error()}
		}
		text, chg := m.toolEnv().edit(a.Path, a.Content)
		return ToolResult{Text: text, Change: chg}
	default:
		return ToolResult{Text: "[unknown tool " + tc.Name + "]"}
	}
}

// runConfirmPlaceholder is the result text a parked RUN carries until the human
// decides. It is a readable placeholder rather than a binary marker: the
// transcript shows what is being asked, and resolveParkedResult swaps in the
// real outcome.
func runConfirmPlaceholder(cmdline string) string {
	return "[RUN awaiting confirmation: " + cmdline + "]"
}

// readFileStr reads a text file, returning "" with nil error for a missing file
// (consistent with the EDIT create flow).
func readFileStr(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// authPath resolves p against the project root and, when RestrictToRoot is
// enabled, refuses paths that escape the root (so the model cannot read or
// write arbitrary files elsewhere).
func (m *Model) authPath(p string) (string, bool) { return m.toolEnv().resolve(p) }

// chatRead reads a file and returns the content for the model.
func (m *Model) chatRead(path string) string { return m.toolEnv().read(path) }

func resolvePath(base, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(base, p)
}

// chatSearch walks the project and returns matching lines as
// "path:line:content" entries. The implementation lives on toolEnv so a
// background sub-agent searches exactly the same way.
func (m *Model) chatSearch(q string, regex bool) string { return m.toolEnv().search(q, regex) }

// runCommand executes a shell command in dir (the project root) with a timeout
// and captures output (capped so a noisy command cannot flood the conversation).
func runCommand(dir, cmdline string) string {
	if cmdline == "" {
		return "[RUN] empty command"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/c", cmdline)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", cmdline)
	}
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "[RUN error] " + err.Error() + "\n" + capCommandOutput(string(out))
	}
	res := capCommandOutput(string(out))
	if res == "" {
		res = "(no output)"
	}
	return "[RUN " + cmdline + "]\n" + res
}

// capCommandOutput truncates command output so a noisy RUN (e.g. a recursive
// listing) cannot bloat memory or the model's context window.
func capCommandOutput(s string) string {
	const limit = 8 * 1024
	if len(s) <= limit {
		return s
	}
	return s[:limit] + fmt.Sprintf("\n… (%d more chars)", len(s)-limit)
}

// isPlausibleText reports whether a file is likely readable text; binary files
// (and compiled build artifacts) are skipped when auto-opening tabs.
func isPlausibleText(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 1024)
	n, _ := f.Read(buf)
	if n == 0 {
		return true // empty file (e.g. freshly created) is fine to open
	}
	return bytes.IndexByte(buf[:n], 0) == -1
}

// fileState captures the size and modification time of a file, used to detect
// both newly created and modified files after a tool round.
type fileState struct {
	size int64
	mod  int64
}

// snapshotFiles walks root and records every regular file (skipping the .git
// directory) together with its size and modification time, so callers can tell
// what a tool created or rewrote while it ran.
func snapshotFiles(root string) map[string]fileState {
	snap := make(map[string]fileState)
	if root == "" {
		return snap
	}
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if filepath.Base(path) == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		snap[path] = fileState{size: info.Size(), mod: info.ModTime().UnixNano()}
		return nil
	})
	return snap
}
