package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()
	if cfg.Editor.WordWrap {
		t.Error("word_wrap should default to false")
	}
	if cfg.Editor.TabWidth != 4 {
		t.Errorf("tab_width = %d, want 4", cfg.Editor.TabWidth)
	}
	if cfg.Editor.SyntaxTheme != "monokai" {
		t.Errorf("syntax_theme = %q, want monokai", cfg.Editor.SyntaxTheme)
	}
	if !cfg.Editor.LineNumbers {
		t.Error("line_numbers should default to true")
	}
	// The out-of-the-box provider is the keyless one; the Ollama URL default
	// still exists for people who pick the local preset.
	if cfg.AI.Provider != PollinationsPreset().Name {
		t.Errorf("provider = %q, want the keyless default", cfg.AI.Provider)
	}
	if cfg.AI.OllamaURL != PollinationsPreset().BaseURL {
		t.Errorf("ollama_url = %q", cfg.AI.OllamaURL)
	}
	if cfg.UI.TreeWidth != 25 {
		t.Errorf("tree_width = %d, want 25", cfg.UI.TreeWidth)
	}
	if cfg.UI.Lang != "en" {
		t.Errorf("lang = %q, want en", cfg.UI.Lang)
	}
}

// TestDefaultsAreSafe pins the security posture of a fresh install: the model
// must not be able to run shell commands or touch files outside the project
// without the human either confirming or explicitly opting out. Both settings
// remain configurable; this only guards against a silent regression to
// permissive defaults.
func TestDefaultsAreSafe(t *testing.T) {
	cfg := Defaults()
	if cfg.AI.AllowRun != "ask" {
		t.Errorf("allow_run = %q, want ask (confirm every command)", cfg.AI.AllowRun)
	}
	if !cfg.AI.RestrictToRoot {
		t.Error("restrict_to_root = false, want true (keep tools inside the project)")
	}
}

// TestPermissiveSafetySettingsAreHonored checks the opt-out path still works,
// so the safer defaults do not become a one-way door.
func TestPermissiveSafetySettingsAreHonored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	body := "[ai]\nallow_run = always\nrestrict_to_root = false\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Load(dir)
	if cfg.AI.AllowRun != "always" {
		t.Errorf("allow_run = %q, want always", cfg.AI.AllowRun)
	}
	if cfg.AI.RestrictToRoot {
		t.Error("restrict_to_root = true, want false")
	}
}

func TestParseINI(t *testing.T) {
	input := `[editor]
tab_width = 2
syntax_theme = dracula
line_numbers = false
word_wrap = true
skipped_dirs = .git,node_modules,vendor

[ai]
model = qwen2.5-coder:7b
ollama_url = http://localhost:11434

[ui]
tree_width = 30
lang = ru
ascii = on
`
	sections := parseINI(strings.NewReader(input))

	if sections["editor"]["tab_width"] != "2" {
		t.Errorf("editor.tab_width = %q, want 2", sections["editor"]["tab_width"])
	}
	if sections["editor"]["word_wrap"] != "true" {
		t.Errorf("editor.word_wrap = %q, want true", sections["editor"]["word_wrap"])
	}
	if sections["editor"]["syntax_theme"] != "dracula" {
		t.Errorf("editor.syntax_theme = %q, want dracula", sections["editor"]["syntax_theme"])
	}
	if sections["ai"]["model"] != "qwen2.5-coder:7b" {
		t.Errorf("ai.model = %q", sections["ai"]["model"])
	}
	if sections["ui"]["tree_width"] != "30" {
		t.Errorf("ui.tree_width = %q", sections["ui"]["tree_width"])
	}
	if sections["ui"]["lang"] != "ru" {
		t.Errorf("ui.lang = %q, want ru", sections["ui"]["lang"])
	}
	if sections["ui"]["ascii"] != "on" {
		t.Errorf("ui.ascii = %q, want on", sections["ui"]["ascii"])
	}
}

func TestParseINIComments(t *testing.T) {
	input := `# this is a comment
; so is this
[editor]
tab_width = 8
`
	sections := parseINI(strings.NewReader(input))
	if sections["editor"]["tab_width"] != "8" {
		t.Errorf("tab_width = %q, want 8", sections["editor"]["tab_width"])
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	content := `[editor]
tab_width = 2
syntax_theme = dracula
word_wrap = true

[ai]
model = llama3
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Defaults()
	loadFile(path, &cfg)

	if cfg.Editor.TabWidth != 2 {
		t.Errorf("tab_width = %d, want 2", cfg.Editor.TabWidth)
	}
	if !cfg.Editor.WordWrap {
		t.Errorf("word_wrap = %v, want true", cfg.Editor.WordWrap)
	}
	if cfg.Editor.SyntaxTheme != "dracula" {
		t.Errorf("syntax_theme = %q, want dracula", cfg.Editor.SyntaxTheme)
	}
	if cfg.AI.Model != "llama3" {
		t.Errorf("model = %q, want llama3", cfg.AI.Model)
	}
	// Defaults should be preserved for unset values
	if cfg.UI.TreeWidth != 25 {
		t.Errorf("tree_width = %d, want 25 (default)", cfg.UI.TreeWidth)
	}
}

func TestLoadProjectOverridesGlobal(t *testing.T) {
	globalDir := t.TempDir()
	globalPath := filepath.Join(globalDir, ".dmed.conf")
	os.WriteFile(globalPath, []byte("[editor]\ntab_width = 2\n"), 0o644)

	projectDir := t.TempDir()
	projectPath := filepath.Join(projectDir, ".dmed.conf")
	os.WriteFile(projectPath, []byte("[editor]\ntab_width = 8\n"), 0o644)

	cfg := Defaults()
	loadFile(globalPath, &cfg)
	loadFile(projectPath, &cfg)

	if cfg.Editor.TabWidth != 8 {
		t.Errorf("tab_width = %d, want 8 (project overrides global)", cfg.Editor.TabWidth)
	}
}

func TestLoadAgentSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	content := "[agent]\nsystem_prompt = You are a refactor expert\ncontext_max = 12345\n"
	os.WriteFile(path, []byte(content), 0o644)

	cfg := Defaults()
	loadFile(path, &cfg)

	if cfg.Agent.SystemPrompt != "You are a refactor expert" {
		t.Errorf("system_prompt = %q", cfg.Agent.SystemPrompt)
	}
	if cfg.Agent.ContextMax != 12345 {
		t.Errorf("context_max = %d, want 12345", cfg.Agent.ContextMax)
	}
}

func TestAgentDefaults(t *testing.T) {
	cfg := Defaults()
	if cfg.Agent.SystemPrompt != "" {
		t.Errorf("default system_prompt should be empty, got %q", cfg.Agent.SystemPrompt)
	}
	if cfg.Agent.ContextMax != 256*1024 {
		t.Errorf("default context_max = %d, want %d", cfg.Agent.ContextMax, 256*1024)
	}
}

func TestPluginsDefaults(t *testing.T) {
	cfg := Defaults()
	if cfg.Plugins.Repo != "dedomorozoff/dmed" || cfg.Plugins.Dir != "plugins" || cfg.Plugins.Branch != "main" {
		t.Errorf("default plugins = %+v", cfg.Plugins)
	}
}

func TestLoadPluginsSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	content := "[plugins]\nrepo = someone/else\ndir = lua\nbranch = dev\n"
	os.WriteFile(path, []byte(content), 0o644)

	cfg := Defaults()
	loadFile(path, &cfg)

	if cfg.Plugins.Repo != "someone/else" || cfg.Plugins.Dir != "lua" || cfg.Plugins.Branch != "dev" {
		t.Errorf("plugins = %+v", cfg.Plugins)
	}
}

func TestWriteAIUpdatesSectionPreservesOthers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	original := `[editor]
tab_width = 2

[ai]
provider = ollama
model = llama3
# keep this comment
system_prompt = keep me

[ui]
tree_width = 20
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	ai := AIConfig{Provider: "openai", Model: "gpt-4o", APIKey: "secret", OllamaURL: "https://api.openai.com/v1", ContextMax: 999}
	n, err := WriteAI(path, ai)
	if err != nil {
		t.Fatal(err)
	}
	if n != 12 {
		t.Errorf("wrote %d keys, want 12", n)
	}

	data, _ := os.ReadFile(path)
	out := string(data)
	for _, want := range []string{"provider = openai", "model = gpt-4o", "api_key = secret",
		"ollama_url = https://api.openai.com/v1", "context_max = 999",
		"system_prompt = keep me", "tab_width = 2", "tree_width = 20"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "provider = ollama") {
		t.Errorf("stale provider left:\n%s", out)
	}
}

func TestWriteAIRetainsMissingKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	if err := os.WriteFile(path, []byte("[ai]\nprovider = ollama\nmodel = llama3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ai := Defaults().AI
	ai.Provider = "Ollama (local)"
	ai.OllamaURL = DefaultOllamaURL
	ai.Model = "llama3"
	n, err := WriteAI(path, ai)
	if err != nil {
		t.Fatal(err)
	}
	if n != 12 {
		t.Errorf("wrote %d keys, want 12 (2 existing + 10 added)", n)
	}
	data, _ := os.ReadFile(path)
	out := string(data)
	for _, want := range []string{"provider = Ollama (local)", "model = llama3", "ollama_url = http://localhost:11434", "context_max = 6000"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

// TestWriteAIPersistsEndpointPrefix: the wizard must save the provider's API
// prefix, otherwise picking Pollinations and restarting the editor would probe
// /v1 and report a 404 as a connection failure.
func TestWriteAIPersistsEndpointPrefix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	p := PollinationsPreset()
	if _, err := WriteAI(path, AIConfig{
		Provider:   p.Name,
		Model:      p.Model,
		OllamaURL:  p.BaseURL,
		APIPath:    p.APIPath,
		ModelsPath: p.ModelsPath,
	}); err != nil {
		t.Fatal(err)
	}
	got := Load(dir)
	if got.AI.APIPath != "/openai" || got.AI.ModelsPath != "/models" {
		t.Fatalf("round-trip lost the endpoint prefix: %q / %q", got.AI.APIPath, got.AI.ModelsPath)
	}
	if got.AI.OllamaURL != p.BaseURL {
		t.Fatalf("url = %q, want %q", got.AI.OllamaURL, p.BaseURL)
	}
}

func TestWriteAICreatesSectionInEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	if _, err := WriteAI(path, Defaults().AI); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	out := string(data)
	if !strings.Contains(out, "[ai]") || !strings.Contains(out, "context_max = 6000") {
		t.Errorf("missing [ai] section or defaults:\n%s", out)
	}
}

func TestLoadAINewKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	content := `[ai]
temperature = 8
num_ctx = 32768
num_predict = 512
tool_rounds = 3
allow_run = never
restrict_to_root = true
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Load(dir)
	if cfg.AI.Temperature != 8 {
		t.Errorf("temperature = %d, want 8", cfg.AI.Temperature)
	}
	if cfg.AI.NumCtx != 32768 {
		t.Errorf("num_ctx = %d, want 32768", cfg.AI.NumCtx)
	}
	if cfg.AI.NumPredict != 512 {
		t.Errorf("num_predict = %d, want 512", cfg.AI.NumPredict)
	}
	if cfg.AI.ToolRounds != 3 {
		t.Errorf("tool_rounds = %d, want 3", cfg.AI.ToolRounds)
	}
	if cfg.AI.AllowRun != "never" {
		t.Errorf("allow_run = %q, want never", cfg.AI.AllowRun)
	}
	if !cfg.AI.RestrictToRoot {
		t.Error("restrict_to_root = false, want true")
	}
}

// TestLoadAIToolLists pins the tool whitelist/blacklist parsing. Both keys are
// hand-edited lists, so the trimming of whitespace and empty entries matters:
// a trailing comma must not turn into an unnamed tool.
func TestLoadAIToolLists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	content := `[ai]
tools_enabled = read, search ,run,
tools_disabled = run , ,
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Load(dir)
	if want := []string{"read", "search", "run"}; !slices.Equal(cfg.AI.ToolsEnabled, want) {
		t.Errorf("tools_enabled = %v, want %v", cfg.AI.ToolsEnabled, want)
	}
	if want := []string{"run"}; !slices.Equal(cfg.AI.ToolsDisabled, want) {
		t.Errorf("tools_disabled = %v, want %v", cfg.AI.ToolsDisabled, want)
	}
}

// TestToolListsDefaultToEverything guards the default: with no configuration
// every tool stays available, because a user who never touched these keys must
// not silently lose EDIT.
func TestToolListsDefaultToEverything(t *testing.T) {
	cfg := Defaults()
	if len(cfg.AI.ToolsEnabled) != 0 || len(cfg.AI.ToolsDisabled) != 0 {
		t.Fatalf("default tool lists = %v / %v, want empty (no filtering)",
			cfg.AI.ToolsEnabled, cfg.AI.ToolsDisabled)
	}
	if !cfg.AI.MCPAsk {
		t.Error("mcp_ask defaults to true: an MCP tool call asks the human first")
	}
}

// TestLoadAIMCPServers pins the open-ended [ai] mcp.<name> = command line,
// the removal-by-empty-value shape copied from [lsp] lang.<id>, and mcp_ask.
func TestLoadAIMCPServers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	content := `[ai]
mcp.files = npx -y @mcp/server-fs .
mcp.db =  postgres-mcp --dsn x
mcp.gone =
mcp_ask = false
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Load(dir)
	if len(cfg.AI.MCPServers) != 3 {
		t.Fatalf("servers = %v, want three entries kept as written", cfg.AI.MCPServers)
	}
	if cfg.AI.MCPServers["files"] != "npx -y @mcp/server-fs ." {
		t.Errorf("files = %q", cfg.AI.MCPServers["files"])
	}
	if cfg.AI.MCPServers["gone"] != "" {
		t.Errorf("an empty value must stay a removal, got %q", cfg.AI.MCPServers["gone"])
	}
	if cfg.AI.MCPAsk {
		t.Error("mcp_ask = false must switch the confirmations off")
	}
	// A configured server is a deliberate setup: the keyless fallback must
	// not fire on top of it.
	if cfg.AI.Unconfigured() {
		t.Error("MCPServers configured must mean Unconfigured() is false")
	}
	// Only a removal left behind does not count as configuration.
	off := Defaults()
	off.AI.MCPServers = map[string]string{"gone": ""}
	if !off.AI.Unconfigured() {
		t.Error("an all-empty MCPServers map must still read as unconfigured")
	}
}

// TestLoadAIWebSearchAndMode covers the opt-in web tool and the agent mode.
func TestLoadAIWebSearchAndMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	content := `[ai]
web_search = true
web_search_budget = 5
mode = plan
api_path = /openai
models_path = /models
free_fallback = false
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Load(dir)
	if !cfg.AI.WebSearch {
		t.Error("web_search = false, want true")
	}
	if cfg.AI.WebSearchBudget != 5 {
		t.Errorf("web_search_budget = %d, want 5", cfg.AI.WebSearchBudget)
	}
	if cfg.AI.AgentMode() != ModePlan {
		t.Errorf("mode = %q, want plan", cfg.AI.Mode_)
	}
	if cfg.AI.APIPath != "/openai" || cfg.AI.ModelsPath != "/models" {
		t.Errorf("api paths = %q / %q", cfg.AI.APIPath, cfg.AI.ModelsPath)
	}
	if cfg.AI.FreeFallback {
		t.Error("free_fallback = true, want false")
	}
}

// TestWebSearchIsOptIn pins the only tool that leaves the workspace: it must be
// off until the user asks for it.
func TestWebSearchIsOptIn(t *testing.T) {
	cfg := Defaults()
	if cfg.AI.WebSearch {
		t.Error("web_search must be off by default")
	}
	if cfg.AI.WebSearchBudget <= 0 {
		t.Errorf("web_search_budget = %d, want a positive cap", cfg.AI.WebSearchBudget)
	}
}

// TestAgentModeDefaultsToAct: a typo in the mode key must not silently turn the
// model into a reader, and a missing key must not either.
func TestAgentModeDefaultsToAct(t *testing.T) {
	if got := Defaults().AI.AgentMode(); got != ModeAct {
		t.Fatalf("default mode = %q, want act", got)
	}
	for _, v := range []string{"", "  ", "planning", "ACT", "nonsense"} {
		ai := Defaults().AI
		ai.Mode_ = v
		if got := ai.AgentMode(); got != ModeAct {
			t.Errorf("mode %q resolved to %q, want act", v, got)
		}
	}
	ai := Defaults().AI
	ai.Mode_ = "  Plan "
	if got := ai.AgentMode(); got != ModePlan {
		t.Errorf("mode %q resolved to %q, want plan", ai.Mode_, got)
	}
}

// TestUnconfiguredGatesTheFreeFallback is the privacy-relevant rule: the editor
// may reach for a keyless public provider only when the user never configured
// anything. Pointing dmed at your own server is a decision, not an oversight.
func TestUnconfiguredGatesTheFreeFallback(t *testing.T) {
	if !Defaults().AI.Unconfigured() {
		t.Error("a fresh install must count as unconfigured")
	}
	aim := Defaults().AI
	aim.Model = "llama3.2"
	if aim.Unconfigured() {
		t.Error("a configured model must disable the fallback")
	}
	aim = Defaults().AI
	aim.APIKey = "sk-test"
	if aim.Unconfigured() {
		t.Error("a configured key must disable the fallback")
	}
	aim = Defaults().AI
	aim.OllamaURL = "http://192.168.1.10:11434"
	if aim.Unconfigured() {
		t.Error("a custom server must disable the fallback")
	}
	aim = Defaults().AI
	aim.Provider = "OpenAI"
	if aim.Unconfigured() {
		t.Error("explicitly choosing a provider is a decision too")
	}
	aim = Defaults().AI
	aim.APIPath = "/v2"
	if aim.Unconfigured() {
		t.Error("a hand-tuned endpoint must disable the fallback")
	}
	aim = Defaults().AI
	aim.ModelsPath = "/v2"
	if aim.Unconfigured() {
		t.Error("a hand-tuned model list must disable the fallback")
	}
	aim = Defaults().AI
	aim.SystemPrompt = "You are my private agent"
	if aim.Unconfigured() {
		t.Error("a custom prompt is a decision too")
	}
	if !Defaults().AI.FreeFallback {
		t.Error("the fallback should be available out of the box")
	}
}

// TestPollinationsPresetNeedsNoKey pins the reason the fallback is possible: the
// provider is keyless and speaks the OpenAI protocol under its own path.
func TestPollinationsPresetNeedsNoKey(t *testing.T) {
	p := PollinationsPreset()
	if p.APIKey {
		t.Error("Pollinations must not ask for a key")
	}
	if !p.Free {
		t.Error("Pollinations must be marked as the keyless fallback")
	}
	if p.Kind != "openai" || p.APIPath != "/openai" || p.ModelsPath != "/models" {
		t.Fatalf("preset = %+v", p)
	}
	names := make([]string, 0, len(AIPresets()))
	for _, p := range AIPresets() {
		names = append(names, p.Name)
	}
	if len(names) < 2 || names[0] != AIPresets()[0].Name {
		t.Fatalf("Ollama must stay the first preset: %v", names)
	}
	if ResolvePreset(p.Name).Name != p.Name {
		t.Error("the preset must be resolvable by name")
	}
}

func TestLoadDebugSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	content := `[debug]
mode = test
program = ./pkg
args = -run TestFoo
stop_on_entry = true
dlv_path = C:\tools\dlv.exe
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Load(dir)
	if cfg.Debug.Mode != "test" {
		t.Errorf("mode = %q, want test", cfg.Debug.Mode)
	}
	if cfg.Debug.Program != "./pkg" {
		t.Errorf("program = %q, want ./pkg", cfg.Debug.Program)
	}
	if cfg.Debug.Args != "-run TestFoo" {
		t.Errorf("args = %q, want -run TestFoo", cfg.Debug.Args)
	}
	if !cfg.Debug.StopOnEntry {
		t.Error("stop_on_entry = false, want true")
	}
	// Legacy dlv_path aliases adapter_cmd.
	if cfg.Debug.AdapterCmd != `C:\tools\dlv.exe` {
		t.Errorf("adapter_cmd = %q, want C:\\tools\\dlv.exe", cfg.Debug.AdapterCmd)
	}

	// Arbitrary modes pass through (non-Delve adapters use their own modes).
	if err := os.WriteFile(path, []byte("[debug]\nmode = bogus\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg = Load(dir)
	if cfg.Debug.Mode != "bogus" {
		t.Errorf("mode = %q, want pass-through bogus", cfg.Debug.Mode)
	}
}

func TestLoadDebugGenericAdapter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	content := `[debug]
adapter_cmd = debugpy-adapter
adapter_mode = stdio
adapter_args = --log-dir /tmp/dap
launch_type = python
launch_request = attach
launch_json = {"justMyCode": false, "console": "integratedTerminal"}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Load(dir)
	if cfg.Debug.AdapterCmd != "debugpy-adapter" {
		t.Errorf("adapter_cmd = %q, want debugpy-adapter", cfg.Debug.AdapterCmd)
	}
	if cfg.Debug.AdapterMode != "stdio" {
		t.Errorf("adapter_mode = %q, want stdio", cfg.Debug.AdapterMode)
	}
	if cfg.Debug.AdapterArgs != "--log-dir /tmp/dap" {
		t.Errorf("adapter_args = %q", cfg.Debug.AdapterArgs)
	}
	if cfg.Debug.LaunchType != "python" {
		t.Errorf("launch_type = %q, want python", cfg.Debug.LaunchType)
	}
	if cfg.Debug.LaunchRequest != "attach" {
		t.Errorf("launch_request = %q, want attach", cfg.Debug.LaunchRequest)
	}
	if cfg.Debug.LaunchJSON != `{"justMyCode": false, "console": "integratedTerminal"}` {
		t.Errorf("launch_json = %q", cfg.Debug.LaunchJSON)
	}
	if err := os.WriteFile(path, []byte("[debug]\nadapter_mode = bogus\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg = Load(dir)
	if cfg.Debug.AdapterMode != "reverse" {
		t.Errorf("adapter_mode = %q, want default reverse for invalid value", cfg.Debug.AdapterMode)
	}
}

func TestLoadDebugConnectMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	content := `[debug]
adapter_mode = connect
adapter_args = 127.0.0.1:9003
launch_type = php
launch_json = {"request": "launch", "type": "php"}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Load(dir)
	if cfg.Debug.AdapterMode != "connect" {
		t.Errorf("adapter_mode = %q, want connect", cfg.Debug.AdapterMode)
	}
	if cfg.Debug.AdapterArgs != "127.0.0.1:9003" {
		t.Errorf("adapter_args = %q", cfg.Debug.AdapterArgs)
	}
	if cfg.Debug.LaunchType != "php" {
		t.Errorf("launch_type = %q, want php", cfg.Debug.LaunchType)
	}
	if cfg.Debug.LaunchJSON != `{"request": "launch", "type": "php"}` {
		t.Errorf("launch_json = %q", cfg.Debug.LaunchJSON)
	}
}

func TestLoadASCIISetting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	if err := os.WriteFile(path, []byte("[ui]\nascii = off\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Load(dir)
	if cfg.UI.Ascii != "off" {
		t.Fatalf("ascii = %q, want off", cfg.UI.Ascii)
	}
	if err := os.WriteFile(path, []byte("[ui]\nascii = bogus\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg = Load(dir)
	if cfg.UI.Ascii != "auto" {
		t.Fatalf("ascii = %q, want default auto for invalid value", cfg.UI.Ascii)
	}
}

func TestWriteLang(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	if err := os.WriteFile(path, []byte("[editor]\ntab_width = 2\n[ui]\ntree_width = 30\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := WriteLang(path, "ru"); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(path)
	s := string(out)
	if !strings.Contains(s, "lang = ru") {
		t.Errorf("lang not set:\n%s", s)
	}
	if !strings.Contains(s, "tab_width = 2") || !strings.Contains(s, "tree_width = 30") {
		t.Errorf("existing content clobbered:\n%s", s)
	}

	// Updating the value must not duplicate the key.
	if err := WriteLang(path, "en"); err != nil {
		t.Fatal(err)
	}
	s2, _ := os.ReadFile(path)
	if strings.Count(string(s2), "lang =") != 1 {
		t.Errorf("lang key duplicated:\n%s", s2)
	}
}

func TestWriteLangCreatesSectionInEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	if err := WriteLang(path, "ru"); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(path)
	s := string(out)
	if !strings.Contains(s, "[ui]") || !strings.Contains(s, "lang = ru") {
		t.Errorf("missing [ui] lang:\n%s", s)
	}
}

func TestLoadKeysSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	content := `[editor]
tab_width = 2

[keys]
# a comment
file.save = ctrl+1
palette.open = ctrl+p, f4
app.quit =
`
	os.WriteFile(path, []byte(content), 0o644)

	cfg := Defaults()
	loadFile(path, &cfg)

	if cfg.Editor.TabWidth != 2 {
		t.Errorf("[keys] ate the sections around it: tab_width = %d", cfg.Editor.TabWidth)
	}
	if got := cfg.Keys.Overrides["file.save"]; len(got) != 1 || got[0] != "ctrl+1" {
		t.Errorf("file.save = %q, want [ctrl+1]", got)
	}
	if got := cfg.Keys.Overrides["palette.open"]; len(got) != 2 || got[1] != "f4" {
		t.Errorf("palette.open = %q, want [ctrl+p f4]", got)
	}
	// The comment line is not an action.
	if _, ok := cfg.Keys.Overrides["# a comment"]; ok {
		t.Error("a comment was parsed as a key binding")
	}
	if len(cfg.Keys.Overrides) != 3 {
		t.Errorf("overrides = %d entries, want 3: %+v", len(cfg.Keys.Overrides), cfg.Keys.Overrides)
	}
}

// An unbind and "the user said nothing" are different statements: one has to
// clear the default chord, the other must leave it alone.
func TestKeysUnbindDiffersFromAbsence(t *testing.T) {
	cfg := Defaults()
	if _, ok := cfg.Keys.Overrides["app.quit"]; ok {
		t.Fatal("Defaults() must not claim every action")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	os.WriteFile(path, []byte("[keys]\napp.quit =\n"), 0o644)
	loadFile(path, &cfg)

	chords, ok := cfg.Keys.Overrides["app.quit"]
	if !ok {
		t.Fatal("the unbind was dropped instead of recorded")
	}
	if len(chords) != 0 {
		t.Errorf("app.quit = %q, want an empty list", chords)
	}
}

func TestSettingsListsKeyOverrides(t *testing.T) {
	cfg := Defaults()
	cfg.Keys.Overrides = map[string][]string{
		"file.save": {"ctrl+1"},
		"app.quit":  nil,
		"palette.open": {
			"ctrl+p", "f4",
		},
	}

	rows := Settings(cfg)
	seen := map[string]string{}
	var order []string
	for _, s := range rows {
		if s.Section == "keys" {
			seen[s.Key] = s.Value
			order = append(order, s.Key)
		}
	}
	if len(seen) != 3 {
		t.Fatalf("keys rows = %d, want 3: %+v", len(seen), rows)
	}
	if seen["file.save"] != "ctrl+1" || seen["palette.open"] != "ctrl+p, f4" {
		t.Errorf("keys rows = %+v", seen)
	}
	// An unbind is listed, not hidden — search-everywhere has to find the line
	// the user wrote in order to tell them what it does.
	if seen["app.quit"] != "(none)" {
		t.Errorf("app.quit = %q, want (none)", seen["app.quit"])
	}
	if !slices.IsSorted(order) {
		t.Errorf("keys rows are not in a stable order: %q", order)
	}
}

// The [theme] grammar is open-ended, so what config owes the user is that the
// words they wrote arrive unchanged: the token name folded the way every other
// key is folded, the style entry kept character for character.
func TestLoadThemeSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	content := `[editor]
tab_width = 2
syntax_theme = dracula

[theme]
# a comment
Keyword = #ff79c6 bold
generic.strong = nobold
comment =
`
	os.WriteFile(path, []byte(content), 0o644)

	cfg := Defaults()
	loadFile(path, &cfg)

	if cfg.Editor.TabWidth != 2 || cfg.Editor.SyntaxTheme != "dracula" {
		t.Errorf("[theme] ate the sections around it: tab_width = %d, syntax_theme = %q",
			cfg.Editor.TabWidth, cfg.Editor.SyntaxTheme)
	}
	if _, ok := cfg.Theme.Tokens["# a comment"]; ok {
		t.Error("a comment was parsed as a token rule")
	}
	if len(cfg.Theme.Tokens) != 3 {
		t.Errorf("tokens = %d entries, want 3: %+v", len(cfg.Theme.Tokens), cfg.Theme.Tokens)
	}
	// The key is folded like every other key is; the value is not touched, because
	// "#ff79c6" and "#FF79C6" are the same colour but "nobold" is not the same word
	// as anything else and a hex is read by its own library.
	if got := cfg.Theme.Tokens["keyword"]; got != "#ff79c6 bold" {
		t.Errorf("keyword = %q, want the entry exactly as written", got)
	}
	if got := cfg.Theme.Tokens["generic.strong"]; got != "nobold" {
		t.Errorf("generic.strong = %q, want nobold", got)
	}
	// An empty value is the absence of a rule, not an error: the line stays in the
	// file so the colour comes back when the value is typed in again.
	if got, ok := cfg.Theme.Tokens["comment"]; !ok || got != "" {
		t.Errorf("comment = %q, ok = %v, want a recorded empty value", got, ok)
	}
}

func TestWriteThemeUpdatesAndKeepsOthers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	if err := os.WriteFile(path, []byte("[editor]\nsyntax_theme = monokai\n[theme]\nkeyword = #111111\nconstant = #222222\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A displayed name is what the picker holds; the file has to end up with the
	// folded spelling parseINI answers to, or one token would have two lines.
	n, err := WriteTheme(path, map[string]string{"Keyword": "#ff0000", "Generic.Strong": "bold"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("WriteTheme wrote %d rules, want 2", n)
	}
	s := readFile(t, path)
	if !strings.Contains(s, "keyword = #ff0000") {
		t.Errorf("the existing rule was not updated:\n%s", s)
	}
	if strings.Contains(s, "Keyword =") {
		t.Errorf("the displayed spelling was written beside the folded one:\n%s", s)
	}
	if strings.Count(s, "keyword =") != 1 {
		t.Errorf("keyword written twice:\n%s", s)
	}
	if !strings.Contains(s, "generic.strong = bold") {
		t.Errorf("a new rule was not added:\n%s", s)
	}
	// The rule the picker knows nothing about is left alone — this is the whole
	// difference between a merge and a rewrite of the user's palette.
	if !strings.Contains(s, "constant = #222222") {
		t.Errorf("an untouched rule was dropped:\n%s", s)
	}
	if !strings.Contains(s, "syntax_theme = monokai") || strings.Contains(s, "[theme]\n[theme]") {
		t.Errorf("the rest of the file was clobbered:\n%s", s)
	}
}

func TestWriteThemeCreatesSectionInEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	if _, err := WriteTheme(path, map[string]string{"keyword": "#ff0000"}); err != nil {
		t.Fatal(err)
	}
	s := readFile(t, path)
	if !strings.Contains(s, "[theme]") || !strings.Contains(s, "keyword = #ff0000") {
		t.Errorf("missing [theme]:\n%s", s)
	}
}

// A rule switched off keeps its line: the value is empty, the token is still
// listed, and the colour comes back the moment a spec is typed into it again.
func TestWriteThemeKeepsAnOffSwitch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	if _, err := WriteTheme(path, map[string]string{"keyword": ""}); err != nil {
		t.Fatal(err)
	}
	if s := readFile(t, path); !strings.Contains(s, "keyword = ") {
		t.Errorf("the off-switch was not written:\n%s", s)
	}
	cfg := Defaults()
	loadFile(path, &cfg)
	if got, ok := cfg.Theme.Tokens["keyword"]; !ok || got != "" {
		t.Errorf("round trip = %q/%v, want the empty value back", got, ok)
	}
}

func TestWriteSyntaxTheme(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	if err := os.WriteFile(path, []byte("[editor]\ntab_width = 2\n[theme]\nkeyword = #111111\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteSyntaxTheme(path, "dracula"); err != nil {
		t.Fatal(err)
	}
	s := readFile(t, path)
	if !strings.Contains(s, "syntax_theme = dracula") || strings.Count(s, "syntax_theme =") != 1 {
		t.Errorf("syntax_theme:\n%s", s)
	}
	if !strings.Contains(s, "tab_width = 2") || !strings.Contains(s, "keyword = #111111") {
		t.Errorf("choosing a style rewrote the palette:\n%s", s)
	}
	if err := WriteSyntaxTheme(path, "monokai"); err != nil {
		t.Fatal(err)
	}
	if s := readFile(t, path); strings.Count(s, "syntax_theme =") != 1 {
		t.Errorf("the base name is stored twice:\n%s", s)
	}
}

func TestSettingsListsThemeRules(t *testing.T) {
	cfg := Defaults()
	cfg.Theme.Tokens = map[string]string{
		"keyword":        "#ff0000",
		"comment":        "",
		"generic.strong": "bold",
	}

	var got []string
	values := map[string]string{}
	for _, s := range Settings(cfg) {
		if s.Section == "theme" {
			got = append(got, s.Key)
			values[s.Key] = s.Value
		}
	}
	if len(got) != 3 {
		t.Fatalf("theme rows = %d, want 3: %+v", len(got), got)
	}
	if values["keyword"] != "#ff0000" || values["generic.strong"] != "bold" {
		t.Errorf("theme rows = %+v", values)
	}
	// A rule switched off is listed as off, not hidden: search-everywhere has to
	// find the line the user wrote and say what it currently does.
	if values["comment"] != "(off)" {
		t.Errorf("comment = %q, want (off)", values["comment"])
	}
	if !slices.IsSorted(got) {
		t.Errorf("theme rows are not in a stable order: %q", got)
	}
}

// readFile is the whole config file as one string: a writer test asserts on the
// text, because what it promises is that the lines it did not touch stay there.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestLoadAgentWorkersAndPersist pins the parallel-agent knobs: workers
// outside 1..4 is a typo, not a preference, so the default survives it, and
// persist=false must silence the store entirely.
func TestLoadAgentWorkersAndPersist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dmed.conf")
	content := `[agent]
workers = 3
persist = false
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Load(dir)
	if cfg.Agent.Workers != 3 {
		t.Errorf("workers = %d, want 3", cfg.Agent.Workers)
	}
	if cfg.Agent.Persist {
		t.Error("persist = false must switch the store off")
	}
	if Defaults().Agent.Workers != 2 || !Defaults().Agent.Persist {
		t.Errorf("defaults = workers %d, persist %v, want 2 and true",
			Defaults().Agent.Workers, Defaults().Agent.Persist)
	}

	// Out of range keeps the default rather than silently running one (or
	// a dozen) workers.
	if err := os.WriteFile(path, []byte("[agent]\nworkers = 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg := Load(dir); cfg.Agent.Workers != 2 {
		t.Errorf("workers = 0 must keep the default, got %d", cfg.Agent.Workers)
	}
	if err := os.WriteFile(path, []byte("[agent]\nworkers = 99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg := Load(dir); cfg.Agent.Workers != 2 {
		t.Errorf("workers = 99 must keep the default, got %d", cfg.Agent.Workers)
	}
}
