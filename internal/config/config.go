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
	Editor EditorConfig
	AI     AIConfig
	Agent  AgentConfig
	UI     UIConfig
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
}

// UIConfig holds UI-related settings.
type UIConfig struct {
	TreeWidth    int
	ChatWidthPct int
	Lang         string
}

// Defaults returns the default configuration.
func Defaults() Config {
	return Config{
		Editor: EditorConfig{
			TabWidth:    4,
			SyntaxTheme: "monokai",
			LineNumbers: true,
			SkippedDirs: []string{".git", "node_modules"},
		},
		AI: AIConfig{
			Provider:   "ollama",
			Model:      "",
			OllamaURL:  "http://localhost:11434",
			ContextMax: 6000,
			SystemPrompt: "You are a helpful coding assistant. " +
				"Answer concisely. When showing code, use markdown fences.",
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
	if v := os.Getenv("DMED_MODEL"); v != "" {
		cfg.AI.Model = v
	}
	if v := os.Getenv("DMED_OLLAMA_URL"); v != "" {
		cfg.AI.OllamaURL = v
	}
	if v := os.Getenv("DMED_SHELL"); v != "" {
		// Shell is not in Config struct but stored separately in the editor.
		// This override is handled by the editor.
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
