// Package ai provides clients for LLM backends with a unified provider
// interface. Supported providers: Ollama (local), OpenAI-compatible (cloud).
package ai

import (
	"context"
	"net/http"
	"strings"
)

// Message is one chat turn.
type Message struct {
	Role    string `json:"role"` // "system" | "user" | "assistant"
	Content string `json:"content"`
}

// Provider is the unified interface for all LLM backends.
type Provider interface {
	// Models returns available model tags from the server.
	Models(ctx context.Context) ([]string, error)
	// ChatStream sends messages and calls onDelta for each content chunk.
	ChatStream(ctx context.Context, msgs []Message, onDelta func(string)) error
}

// ProviderType identifies a backend implementation.
type ProviderType string

const (
	OllamaProvider ProviderType = "ollama"
	OpenAIProvider ProviderType = "openai"
)

// Config holds the configuration for creating a provider.
type Config struct {
	Type    ProviderType // ollama | openai
	URL     string       // base URL
	Model   string       // model tag
	APIKey  string       // API key (OpenAI only)
}

// NewProvider creates a Provider based on the given config.
func NewProvider(cfg Config) Provider {
	if cfg.URL == "" {
		switch cfg.Type {
		case OpenAIProvider:
			cfg.URL = "https://api.openai.com"
		default:
			cfg.URL = "http://localhost:11434"
		}
	}
	cfg.URL = strings.TrimRight(cfg.URL, "/")

	httpClient := &http.Client{}

	switch cfg.Type {
	case OpenAIProvider:
		return &openAIProvider{
			url:    cfg.URL,
			model:  cfg.Model,
			apiKey: cfg.APIKey,
			http:   httpClient,
		}
	default:
		return &ollamaProvider{
			url:   cfg.URL,
			model: cfg.Model,
			http:  httpClient,
		}
	}
}
