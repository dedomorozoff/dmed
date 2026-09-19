package config

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds all editor configuration.
type Config struct {
	Editor  EditorConfig
	AI      AIConfig
	Agent   AgentConfig
	UI      UIConfig
	Plugins PluginsConfig
	Debug   DebugConfig
	LSP     LSPConfig
}

// DebugConfig holds DAP debugger settings (M7). Go/Delve is the default
// adapter; any other DAP adapter (debugpy, lldb-dap, node, ...) can be wired
// in through the Adapter*/Launch* fields.
type DebugConfig struct {
	// Mode is the DAP launch mode passed through to the adapter ("debug" for
	// Delve: build+run, "test": go test, "exec": run a prebuilt binary).
	Mode string
	// Program is the package directory, test package, executable or module
	// entrypoint to debug. Empty derives it from the active file's directory.
	Program string
	// Args are whitespace-separated arguments passed to the debuggee.
	Args string
	// StopOnEntry pauses at the first instruction after launch.
	StopOnEntry bool

	// AdapterCmd is the DAP adapter executable ("dlv", "debugpy-adapter",
	// "node", ...). Defaults to "dlv"; the legacy `dlv_path` key is an alias.
	AdapterCmd string
	// AdapterMode is how the adapter transports DAP: "reverse" (the adapter
	// dials us back, Delve-style) or "stdio" (the adapter speaks DAP on
	// stdin/stdout — the common layout for debugpy, lldb-dap, etc.).
	AdapterMode string
	// AdapterArgs are extra whitespace-separated CLI arguments for the
	// adapter process itself (e.g. `--host 127.0.0.1`).
	AdapterArgs string

	// LaunchType is the launch request "type" field (default "go").
	LaunchType string
	// LaunchRequest is the launch request kind: "launch" (default) or "attach".
	LaunchRequest string
	// LaunchJSON is an optional raw JSON object merged into the launch/attach
	// arguments; adapter-specific keys override the built-in ones.
	LaunchJSON string
}

// LSPConfig holds language-server integration settings.
//
// The master switch (Enabled) disables every language server at once; the
// Disabled map turns individual languages off by their LSP language id
// ("go", "python", "typescript", ...). Servers that are not installed on
// PATH are skipped regardless, so these are purely opt-out toggles.
type LSPConfig struct {
	Enabled  bool            // master switch; default true = LSP on everywhere
	Disabled map[string]bool // language id => LSP switched off for it
}

// AgentConfig holds settings for background agent tasks (M4).
type AgentConfig struct {
	// SystemPrompt overrides the default instruction the agent follows when
	// producing file edits. Empty uses the built-in prompt.
	SystemPrompt string
	// ContextMax is the total size budget (bytes) of file context gathered
	// from the project and sent to the agent.
	ContextMax int
}

// EditorConfig holds editor-related settings.
type EditorConfig struct {
	TabWidth    int
	SyntaxTheme string
	LineNumbers bool
	WordWrap    bool
	SkippedDirs []string
}

// AIConfig holds AI-related settings.
type AIConfig struct {
	Provider     string // ollama | openai
	Model        string
	OllamaURL    string
	APIKey       string
	SystemPrompt string
	ContextMax   int
	// Temperature is in tenths (7 => 0.7); 0 uses the provider default.
	Temperature int
	// NumCtx is the model context window in tokens (ollama `num_ctx`); 0 = default.
	NumCtx int
	// NumPredict is the max output tokens; 0 = provider default.
	NumPredict int
	// ToolRounds caps the chat tool-calling loop depth; 0 uses the built-in cap (6).
	ToolRounds int
	// AllowRun: "always" runs the model's commands, "never" blocks them, and
	// "ask" pauses for explicit per-command confirmation.
	AllowRun string
	// RestrictToRoot bounds READ/EDIT/REPLACE paths to the project root when true.
	RestrictToRoot bool
}

// UIConfig holds UI-related settings.
type UIConfig struct {
	TreeWidth    int
	ChatWidthPct int
	Lang         string
}

// PluginsConfig configures the remote plugin store. Plugins are listed and
// installed from a GitHub repo's plugins/ directory.
type PluginsConfig struct {
	Repo   string // "owner/repo"
	Dir    string // path inside the repo holding .lua plugins
	Branch string // branch to read from
}

// Defaults returns the default configuration.
func Defaults() Config {
	return Config{
		Editor: EditorConfig{
			TabWidth:    4,
			SyntaxTheme: "monokai",
			LineNumbers: true,
			WordWrap:    false,
			SkippedDirs: []string{".git", "node_modules"},
		},
		AI: AIConfig{
			Provider:       "ollama",
			Model:          "",
			OllamaURL:      "http://localhost:11434",
			ContextMax:     6000,
			Temperature:    0,
			NumCtx:         0,
			NumPredict:     0,
			ToolRounds:     0,
			AllowRun:       "always",
			RestrictToRoot: false,
			SystemPrompt: "You are a helpful coding assistant inside the dmed editor. " +
				"Answer concisely. You have tools: EDIT creates or rewrites a whole file, " +
				"READ reads a file, SEARCH finds text, RUN executes a shell command. " +
				"When the user asks to create, change or fix files, you MUST call EDIT " +
				"(after READ for existing files) instead of printing code in the reply.",
		},
		Agent: AgentConfig{
			SystemPrompt: "",
			ContextMax:   256 * 1024,
		},
		UI: UIConfig{
			TreeWidth:    25,
			ChatWidthPct: 40,
			Lang:         "en",
		},
		Plugins: PluginsConfig{
			Repo:   "dedomorozoff/dmed",
			Dir:    "plugins",
			Branch: "main",
		},
		Debug: DebugConfig{
			Mode:          "debug",
			Program:       "",
			Args:          "",
			StopOnEntry:   false,
			AdapterCmd:    "dlv",
			AdapterMode:   "reverse",
			AdapterArgs:   "",
			LaunchType:    "go",
			LaunchRequest: "launch",
			LaunchJSON:    "",
		},
		LSP: LSPConfig{
			Enabled:  true,
			Disabled: map[string]bool{},
		},
	}
}

// Load reads configuration from disk and applies environment variable overrides.
// Priority: defaults < global config < project config < env vars.
func Load(projectRoot string) Config {
	cfg := Defaults()

	// Load global config
	if home, err := os.UserHomeDir(); err == nil {
		globalPath := filepath.Join(home, ".dmed.conf")
		loadFile(globalPath, &cfg)
	}

	// Load project config (overrides global)
	if projectRoot != "" {
		projectPath := filepath.Join(projectRoot, ".dmed.conf")
		loadFile(projectPath, &cfg)
	}

	// Environment variable overrides
	if v := os.Getenv("DMED_PROVIDER"); v != "" {
		cfg.AI.Provider = v
	}
	if v := os.Getenv("DMED_MODEL"); v != "" {
		cfg.AI.Model = v
	}
	if v := os.Getenv("DMED_OLLAMA_URL"); v != "" {
		cfg.AI.OllamaURL = v
	}
	if v := os.Getenv("DMED_API_KEY"); v != "" {
		cfg.AI.APIKey = v
	}
	if v := os.Getenv("DMED_SHELL"); v != "" {
		// Shell is not in Config struct but stored separately in the editor.
		// This override is handled by the editor.
	}
	if v := os.Getenv("DMED_LANG"); v != "" {
		cfg.UI.Lang = v
	}
	if v := os.Getenv("DMED_PLUGIN_REPO"); v != "" {
		cfg.Plugins.Repo = v
	}
	if v := os.Getenv("DMED_TEMPERATURE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.AI.Temperature = n
		}
	}
	if v := os.Getenv("DMED_NUM_CTX"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.AI.NumCtx = n
		}
	}
	if v := os.Getenv("DMED_NUM_PREDICT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.AI.NumPredict = n
		}
	}
	if v := os.Getenv("DMED_TOOL_ROUNDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.AI.ToolRounds = n
		}
	}
	if v := os.Getenv("DMED_ALLOW_RUN"); v != "" {
		if v == "always" || v == "never" || v == "ask" {
			cfg.AI.AllowRun = v
		}
	}

	return cfg
}

// WriteLang sets the `[ui] lang` value in the INI file at path, preserving all
// other content. If the file or the [ui] section is missing it is appended.
func WriteLang(path, lang string) error {
	data, err := os.ReadFile(path)
	var lines []string
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		lines = strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	}

	var out []string
	inUI := false
	uiPresent := false
	wrote := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			name := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
			inUI = strings.EqualFold(name, "ui")
			if inUI {
				uiPresent = true
			}
		}
		if inUI {
			if idx := strings.IndexByte(line, '='); idx > 0 {
				if strings.ToLower(strings.TrimSpace(line[:idx])) == "lang" {
					out = append(out, "lang = "+lang)
					wrote = true
					continue
				}
			}
		}
		out = append(out, line)
	}

	if !wrote {
		if !uiPresent {
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			out = append(out, "[ui]")
		}
		out = append(out, "lang = "+lang)
	}

	content := strings.Join(out, "\n") + "\n"
	if len(content) == 1 {
		content = ""
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// ConfigPath returns the path to the global config file.
func ConfigPath() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".dmed.conf")
	}
	return ".dmed.conf"
}

// ProjectConfigPath returns the path to the project-level config file.
func ProjectConfigPath(root string) string {
	if root != "" {
		return filepath.Join(root, ".dmed.conf")
	}
	return ""
}

func loadFile(path string, cfg *Config) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sections := parseINI(f)

	// [editor]
	if s, ok := sections["editor"]; ok {
		if v, ok := s["tab_width"]; ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.Editor.TabWidth = n
			}
		}
		if v, ok := s["syntax_theme"]; ok {
			cfg.Editor.SyntaxTheme = v
		}
		if v, ok := s["line_numbers"]; ok {
			cfg.Editor.LineNumbers = parseBool(v)
		}
		if v, ok := s["word_wrap"]; ok {
			cfg.Editor.WordWrap = parseBool(v)
		}
		if v, ok := s["skipped_dirs"]; ok {
			cfg.Editor.SkippedDirs = strings.Split(v, ",")
			for i := range cfg.Editor.SkippedDirs {
				cfg.Editor.SkippedDirs[i] = strings.TrimSpace(cfg.Editor.SkippedDirs[i])
			}
		}
	}

	// [ai]
	if s, ok := sections["ai"]; ok {
		if v, ok := s["provider"]; ok {
			cfg.AI.Provider = v
		}
		if v, ok := s["model"]; ok {
			cfg.AI.Model = v
		}
		if v, ok := s["ollama_url"]; ok {
			cfg.AI.OllamaURL = v
		}
		if v, ok := s["api_key"]; ok {
			cfg.AI.APIKey = v
		}
		if v, ok := s["system_prompt"]; ok {
			cfg.AI.SystemPrompt = v
		}
		if v, ok := s["context_max"]; ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.AI.ContextMax = n
			}
		}
		if v, ok := s["temperature"]; ok {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				cfg.AI.Temperature = n
			}
		}
		if v, ok := s["num_ctx"]; ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.AI.NumCtx = n
			}
		}
		if v, ok := s["num_predict"]; ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.AI.NumPredict = n
			}
		}
		if v, ok := s["tool_rounds"]; ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.AI.ToolRounds = n
			}
		}
		if v, ok := s["allow_run"]; ok {
			if v == "always" || v == "never" || v == "ask" {
				cfg.AI.AllowRun = v
			}
		}
		if v, ok := s["restrict_to_root"]; ok {
			cfg.AI.RestrictToRoot = parseBool(v)
		}
	}

	// [agent]
	if s, ok := sections["agent"]; ok {
		if v, ok := s["system_prompt"]; ok {
			cfg.Agent.SystemPrompt = v
		}
		if v, ok := s["context_max"]; ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.Agent.ContextMax = n
			}
		}
	}

	// [ui]
	if s, ok := sections["ui"]; ok {
		if v, ok := s["tree_width"]; ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.UI.TreeWidth = n
			}
		}
		if v, ok := s["chat_width_pct"]; ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 80 {
				cfg.UI.ChatWidthPct = n
			}
		}
		if v, ok := s["lang"]; ok {
			cfg.UI.Lang = v
		}
	}

	// [plugins]
	if s, ok := sections["plugins"]; ok {
		if v, ok := s["repo"]; ok {
			cfg.Plugins.Repo = v
		}
		if v, ok := s["dir"]; ok {
			cfg.Plugins.Dir = v
		}
		if v, ok := s["branch"]; ok {
			cfg.Plugins.Branch = v
		}
	}

	// [debug]
	if s, ok := sections["debug"]; ok {
		if v, ok := s["mode"]; ok && v != "" {
			cfg.Debug.Mode = v
		}
		if v, ok := s["program"]; ok {
			cfg.Debug.Program = v
		}
		if v, ok := s["args"]; ok {
			cfg.Debug.Args = v
		}
		if v, ok := s["stop_on_entry"]; ok {
			cfg.Debug.StopOnEntry = parseBool(v)
		}
		// `dlv_path` is the legacy alias for `adapter_cmd`.
		if v, ok := s["adapter_cmd"]; ok {
			cfg.Debug.AdapterCmd = v
		} else if v, ok := s["dlv_path"]; ok {
			cfg.Debug.AdapterCmd = v
		}
		if v, ok := s["adapter_mode"]; ok {
			if v == "reverse" || v == "stdio" {
				cfg.Debug.AdapterMode = v
			}
		}
		if v, ok := s["adapter_args"]; ok {
			cfg.Debug.AdapterArgs = v
		}
		if v, ok := s["launch_type"]; ok {
			cfg.Debug.LaunchType = v
		}
		if v, ok := s["launch_request"]; ok {
			if v == "launch" || v == "attach" {
				cfg.Debug.LaunchRequest = v
			}
		}
		if v, ok := s["launch_json"]; ok {
			cfg.Debug.LaunchJSON = v
		}
	}

	// [lsp] — master switch plus per-language opt-out toggles.
	if s, ok := sections["lsp"]; ok {
		if v, ok := s["enabled"]; ok {
			cfg.LSP.Enabled = parseBool(v)
		}
		if cfg.LSP.Disabled == nil {
			cfg.LSP.Disabled = map[string]bool{}
		}
		for k, v := range s {
			if k == "enabled" {
				continue
			}
			if parseBool(v) {
				cfg.LSP.Disabled[k] = true
			}
		}
	}
}

// parseINI reads an INI file and returns section -> key -> value.
func parseINI(r io.Reader) map[string]map[string]string {
	sections := make(map[string]map[string]string)
	current := ""

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		// Section header
		if line[0] == '[' && line[len(line)-1] == ']' {
			current = strings.ToLower(line[1 : len(line)-1])
			if _, ok := sections[current]; !ok {
				sections[current] = make(map[string]string)
			}
			continue
		}
		// Key = value
		if idx := strings.IndexByte(line, '='); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			val := strings.TrimSpace(line[idx+1:])
			// Strip quotes
			if len(val) >= 2 && (val[0] == '"' && val[len(val)-1] == '"' || val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
			if current == "" {
				current = "_"
				sections[current] = make(map[string]string)
			}
			sections[current][strings.ToLower(key)] = val
		}
	}
	return sections
}

func parseBool(s string) bool {
	s = strings.ToLower(s)
	return s == "true" || s == "yes" || s == "1"
}

// WriteAI merges the AI settings into the INI file at path, updating the
// [ai] section in place and preserving all other sections, keys, and comments.
// If the file or the [ai] section is missing it is appended. Returns the
// number of keys written.
func WriteAI(path string, ai AIConfig) (int, error) {
	known := []struct{ key, val string }{
		{"provider", ai.Provider},
		{"model", ai.Model},
		{"ollama_url", ai.OllamaURL},
		{"api_key", ai.APIKey},
		{"context_max", strconv.Itoa(ai.ContextMax)},
		{"temperature", strconv.Itoa(ai.Temperature)},
		{"num_ctx", strconv.Itoa(ai.NumCtx)},
		{"num_predict", strconv.Itoa(ai.NumPredict)},
		{"tool_rounds", strconv.Itoa(ai.ToolRounds)},
		{"allow_run", ai.AllowRun},
	}

	data, err := os.ReadFile(path)
	var lines []string
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	if err == nil {
		lines = strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	}

	var out []string
	inAI := false
	aiPresent := false
	replaced := make(map[string]bool, len(known))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			name := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
			inAI = strings.EqualFold(name, "ai")
			if inAI {
				aiPresent = true
			}
		}
		if inAI {
			if idx := strings.IndexByte(line, '='); idx > 0 {
				key := strings.ToLower(strings.TrimSpace(line[:idx]))
				matched := false
				for _, k := range known {
					if k.key == key {
						out = append(out, key+" = "+k.val)
						replaced[key] = true
						matched = true
						break
					}
				}
				if matched {
					continue
				}
			}
		}
		out = append(out, line)
	}

	var missing []string
	for _, k := range known {
		if !replaced[k.key] {
			missing = append(missing, k.key+" = "+k.val)
		}
	}
	if !aiPresent {
		if len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
		}
		out = append(out, "[ai]")
		out = append(out, missing...)
	} else if len(missing) > 0 {
		for i := len(out) - 1; i >= 0; i-- {
			t := strings.TrimSpace(out[i])
			if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") &&
				strings.EqualFold(strings.TrimSpace(t[1:len(t)-1]), "ai") {
				tail := append([]string{}, out[i+1:]...)
				out = append(append(out[:i+1], missing...), tail...)
				break
			}
		}
	}

	content := strings.Join(out, "\n") + "\n"
	if len(content) == 1 {
		content = ""
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return 0, err
	}
	return len(replaced) + len(missing), nil
}

// writeSection merges the given key/value pairs into the INI section at path,
// preserving all other sections, keys and comments. A missing section or file
// is appended. It returns the number of keys written. This is the shared
// engine behind WriteAI, WriteDebug and WriteLSP.
func writeSection(path, section string, known [][2]string) (int, error) {
	data, err := os.ReadFile(path)
	var lines []string
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	if err == nil {
		lines = strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	}

	var out []string
	inSec := false
	secPresent := false
	replaced := make(map[string]bool, len(known))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			name := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
			inSec = strings.EqualFold(name, section)
			if inSec {
				secPresent = true
			}
		}
		if inSec {
			if idx := strings.IndexByte(line, '='); idx > 0 {
				key := strings.ToLower(strings.TrimSpace(line[:idx]))
				matched := false
				for _, k := range known {
					if k[0] == key {
						out = append(out, key+" = "+k[1])
						replaced[key] = true
						matched = true
						break
					}
				}
				if matched {
					continue
				}
			}
		}
		out = append(out, line)
	}

	var missing []string
	for _, k := range known {
		if !replaced[k[0]] {
			missing = append(missing, k[0]+" = "+k[1])
		}
	}
	if !secPresent {
		if len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
		}
		out = append(out, "["+section+"]")
		out = append(out, missing...)
	} else if len(missing) > 0 {
		for i := len(out) - 1; i >= 0; i-- {
			t := strings.TrimSpace(out[i])
			if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") &&
				strings.EqualFold(strings.TrimSpace(t[1:len(t)-1]), section) {
				tail := append([]string{}, out[i+1:]...)
				out = append(append(out[:i+1], missing...), tail...)
				break
			}
		}
	}

	content := strings.Join(out, "\n") + "\n"
	if len(content) == 1 {
		content = ""
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return 0, err
	}
	return len(replaced) + len(missing), nil
}

// WriteDebug merges the DAP/[debug] settings into the INI file at path,
// updating the [debug] section in place and preserving everything else.
func WriteDebug(path string, d DebugConfig) (int, error) {
	known := [][2]string{
		{"adapter_cmd", d.AdapterCmd},
		{"adapter_mode", d.AdapterMode},
		{"adapter_args", d.AdapterArgs},
		{"launch_type", d.LaunchType},
		{"launch_request", d.LaunchRequest},
		{"mode", d.Mode},
		{"program", d.Program},
		{"args", d.Args},
		{"stop_on_entry", boolStr(d.StopOnEntry)},
		{"launch_json", d.LaunchJSON},
	}
	return writeSection(path, "debug", known)
}

// WriteLSP merges the LSP settings into the INI file at path, updating the
// [lsp] section in place. Every known language id is written so a toggled-off
// server stays off across edits; unknown ids already present are kept.
func WriteLSP(path string, l LSPConfig) (int, error) {
	if l.Disabled == nil {
		l.Disabled = map[string]bool{}
	}
	known := make([][2]string, 0, len(LSPLanguages)+1)
	known = append(known, [2]string{"enabled", boolStr(l.Enabled)})
	seen := map[string]bool{"enabled": true}
	for _, lang := range LSPLanguages {
		val := "false"
		if l.Disabled[lang] {
			val = "true"
		}
		known = append(known, [2]string{lang, val})
		seen[lang] = true
	}
	// Preserve any user-added unknown language keys in the written state.
	for k, v := range l.Disabled {
		if !seen[k] {
			known = append(known, [2]string{k, boolStr(v)})
			seen[k] = true
		}
	}
	return writeSection(path, "lsp", known)
}

// LSPLanguages lists every language id the editor knows how to power through
// a language server, in a stable display order. Used by the [lsp] config
// writer and the LSP settings wizard.
var LSPLanguages = []string{
	"go", "python", "typescript", "rust", "c", "cpp", "lua",
	"ruby", "php", "json", "yaml", "css", "html", "zig",
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// AIPreset describes one built-in "just works" provider entry a beginner can
// pick without reading docs: choosing it fills the base URL (and a sensible
// default model when the provider exposes a stable one) so only the API key
// is left to type. The base URL is the origin only — internal/ai appends the
// API paths (/v1/chat/completions, /v1/models) itself.
type AIPreset struct {
	Name    string // display name shown in the wizard
	Kind    string // wire protocol: "ollama" | "openai"
	BaseURL string // origin, no path suffix ("" = keep the current URL)
	Model   string // optional suggested model ("" = resolved from the server)
	APIKey  bool   // whether this provider needs an API key
}

// DefaultOllamaURL is the address a stock local Ollama install listens on.
// Exported so the wizard test button and the CLI setup can share the hint.
const DefaultOllamaURL = "http://localhost:11434"

// AIPresets lists the built-in providers in wizard cycle order. Ollama comes
// first: it is free, local and needs no key, making it the best beginner path.
// The last entry is Custom — it keeps whatever URL/model the user already had.
func AIPresets() []AIPreset {
	return []AIPreset{
		{Name: "Ollama (local)", Kind: "ollama", BaseURL: DefaultOllamaURL},
		{Name: "OpenAI", Kind: "openai", BaseURL: "https://api.openai.com", Model: "gpt-4o-mini", APIKey: true},
		{Name: "DeepSeek", Kind: "openai", BaseURL: "https://api.deepseek.com", Model: "deepseek-chat", APIKey: true},
		{Name: "Groq", Kind: "openai", BaseURL: "https://api.groq.com", Model: "llama-3.3-70b-versatile", APIKey: true},
		{Name: "LM Studio (local)", Kind: "openai", BaseURL: "http://localhost:1234"},
		{Name: "vLLM (local)", Kind: "openai", BaseURL: "http://localhost:8000"},
		{Name: "Custom", Kind: "openai", BaseURL: ""},
	}
}

// ResolvePreset returns the preset matching a stored provider label, falling
// back to Ollama for unknown/empty values so a hand-edited config never leaves
// the wizard stuck on a name it cannot cycle from.
func ResolvePreset(name string) AIPreset {
	for _, p := range AIPresets() {
		if strings.EqualFold(p.Name, name) {
			return p
		}
	}
	return AIPresets()[0]
}
