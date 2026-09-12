// Package ai provides clients for LLM backends with a unified provider
// interface. Supported providers: Ollama (local), OpenAI-compatible (cloud).
package ai

import (
	"context"
	"net/http"
	"strings"
)

// Message is one chat turn. For an assistant message that invoked tools,
// Content may be empty and ToolCalls holds the invocations; for a role "tool"
// message, ToolCallID links back to the assistant's call and ToolName carries
// the function name (required by Ollama).
type Message struct {
	Role       string // "system" | "user" | "assistant" | "tool"
	Content    string
	ToolCalls  []ToolCall // assistant: functions invoked by the model
	ToolCallID string     // tool: id of the assistant call being answered
	ToolName   string     // tool: function name (Ollama needs it explicitly)
}

// ToolDef describes one callable tool the model may invoke.
type ToolDef struct {
	Name        string
	Description string
	Parameters  any // JSON Schema object describing the arguments
}

// ToolCall is one function invocation emitted by the model.
type ToolCall struct {
	ID   string // call id (OpenAI); synthesized for Ollama
	Name string // function name
	Args string // JSON-encoded arguments object
}

// Options carries generation parameters. All fields are 0 when unset, meaning
// "use the provider default" (this keeps Request literals terse). Temperature
// is in tenths (e.g. 8 => 0.8) so config parsing can stay on integers.
type Options struct {
	Temperature int // temperature in tenths; 0 = provider default
	NumCtx      int // ollama context window in tokens; 0 = provider default
	NumPredict  int // max output tokens; 0 = provider default
}

// Request bundles the conversation, the tools available for one turn, and the
// optional generation options.
type Request struct {
	Messages []Message
	Tools    []ToolDef
	Options  Options
}

// Handler delivers the streamed content and, once complete, any tool calls
// the model produced.
type Handler struct {
	Delta     func(string)     // called for each content chunk
	ToolCalls func([]ToolCall) // called at most once per turn, if any
}

// Provider is the unified interface for all LLM backends.
type Provider interface {
	// Models returns available model tags from the server.
	Models(ctx context.Context) ([]string, error)
	// ChatStream streams a response for the request, invoking h.Delta for each
	// content chunk and h.ToolCalls when the model finishes with tool calls.
	ChatStream(ctx context.Context, req Request, h Handler) error
}

// ProviderType identifies a backend implementation.
type ProviderType string

const (
	OllamaProvider ProviderType = "ollama"
	OpenAIProvider ProviderType = "openai"
)

// Config holds the configuration for creating a provider.
type Config struct {
	Type   ProviderType // ollama | openai
	URL    string       // base URL
	Model  string       // model tag
	APIKey string       // API key (OpenAI only)
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
	// The provider appends /v1/... paths itself; beginners pasting endpoints
	// like https://api.deepseek.com/v1 (or LM Studio's advertised URL) would
	// otherwise hit /v1/v1/chat/completions. Strip a trailing /v1 so both the
	// bare origin and the full endpoint form work.
	if cfg.Type == OpenAIProvider && strings.HasSuffix(cfg.URL, "/v1") {
		cfg.URL = strings.TrimSuffix(cfg.URL, "/v1")
	}

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
