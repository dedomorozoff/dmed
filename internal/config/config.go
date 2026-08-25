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
	UI     UIConfig
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
		UI: UIConfig{
			TreeWidth:    25,
			ChatWidthPct: 40,
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
