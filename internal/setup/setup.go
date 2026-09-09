// Package setup implements the interactive `dmed setup-ai` onboarding: it
// walks a beginner through the built-in provider presets, API key and model,
// verifies the connection and only then persists the result to the global
// config via config.WriteAI. The TUI wizard (AI: Preferences) edits the same
// keys; this command is the no-editor fast path.
package setup

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"dmed/internal/ai"
	"dmed/internal/config"
)

// Run walks the user through AI setup and writes ~/.dmed.conf. Every prompt
// accepts Enter to keep the current/default value, so a stock Ollama user
// gets through with a few key presses.
func Run() error {
	presets := config.AIPresets()
	in := bufio.NewScanner(os.Stdin)

	fmt.Println("dmed AI setup — pick a provider (Enter = Ollama, free & local):")
	for i, p := range presets {
		need := ""
		if p.APIKey {
			need = "  [API key]"
		}
		fmt.Printf("  %d) %-18s %s%s\n", i+1, p.Name, p.BaseURL, need)
	}
	idx := askNumber(in, "provider", 1, len(presets), 1) - 1
	p := presets[idx]

	cfg := config.Load("")
	provider := p.Name
	url := p.BaseURL
	if url == "" { // Custom: keep the currently configured URL
		url = cfg.AI.OllamaURL
	}

	apiKey := cfg.AI.APIKey
	if p.APIKey {
		fmt.Print("API key (visible while typing): ")
		if in.Scan() {
			if v := strings.TrimSpace(in.Text()); v != "" {
				apiKey = v
			}
		}
	}

	model := cfg.AI.Model
	if p.Model != "" && model == "" {
		model = p.Model
	}

	// Probe with the chosen settings; auto-pick a model for local servers.
	prov := ai.NewProvider(ai.Config{Type: ai.ProviderType(p.Kind), URL: url, Model: model, APIKey: apiKey})
	fmt.Print("testing connection... ")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	models, err := prov.Models(ctx)
	switch {
	case err != nil:
		fmt.Printf("failed: %v\n", err)
		fmt.Println("Settings are NOT saved. Start the server (e.g. `ollama serve`)")
		fmt.Println("and re-run `dmed setup-ai`, or configure by hand: run dmed,")
		fmt.Println("Ctrl+P, 'AI: Preferences'.")
		return nil
	case len(models) == 0:
		fmt.Println("connected, but the server reports no models.")
	default:
		fmt.Printf("ok, %d model(s) found\n", len(models))
		if model == "" {
			model = models[0]
			fmt.Println("  using: " + model)
		} else if !containsFold(models, model) {
			fmt.Println("  note: " + model + " is not in the server's model list:")
			for _, mm := range models {
				fmt.Println("    - " + mm)
			}
		}
	}

	path := config.ConfigPath()
	if _, err := config.WriteAI(path, config.AIConfig{
		Provider:   provider,
		Model:      model,
		OllamaURL:  url,
		APIKey:     apiKey,
		ContextMax: cfg.AI.ContextMax,
	}); err != nil {
		return err
	}
	fmt.Println("saved to " + path)
	fmt.Println("Run `dmed`, press Alt+A and ask something.")
	return nil
}

// askNumber prints a numbered-choice prompt and re-asks until the answer is a
// number in [min, max]; empty input or EOF returns def.
func askNumber(in *bufio.Scanner, label string, min, max, def int) int {
	for {
		fmt.Printf("%s [%d-%d, default %d]: ", label, min, max, def)
		if !in.Scan() {
			return def
		}
		t := strings.TrimSpace(in.Text())
		if t == "" {
			return def
		}
		n, err := strconv.Atoi(t)
		if err == nil && n >= min && n <= max {
			return n
		}
		fmt.Printf("  enter a number between %d and %d\n", min, max)
	}
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}
