// Package ai provides clients for LLM backends with a unified provider
// interface. Supported providers: Ollama (local), OpenAI-compatible (cloud).
package ai

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
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
	// APIPath is the path prefix of the OpenAI-compatible endpoints. Almost
	// every server uses /v1, but Pollinations serves the same protocol under
	// /openai, so the prefix is configurable instead of hardcoded.
	APIPath string
	// ModelsPath overrides the model-list path when it differs from
	// APIPath + "/models" (Pollinations lists its text models at /models).
	ModelsPath string
}

// Stream timeouts are deliberately split: the header wait is bounded so a
// dead server cannot pin a chat forever, while the body itself has no overall
// deadline — a long generation must not be cut off mid-answer. Cancellation
// is driven by the request context (ctx), which the editor wires to Esc.
const (
	connectTimeout    = 10 * time.Second
	responseHdrTimout = 30 * time.Second
)

// newHTTPClient builds the shared client. No http.Client.Timeout is set,
// because it would also abort an in-flight stream.
func newHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: connectTimeout}).DialContext,
			TLSHandshakeTimeout:   connectTimeout,
			ResponseHeaderTimeout: responseHdrTimout,
			ExpectContinueTimeout: time.Second,
			IdleConnTimeout:       90 * time.Second,
		},
	}
}

// NewProvider creates a Provider based on the given config.
// hostOf returns the host[:port] of a base URL, for the per-host request gate.
// An unparsable URL yields the whole string, which only costs gate sharing.
func hostOf(u string) string {
	if parsed, err := url.Parse(u); err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return u
}

// normalizeProviderURL corrects the Pollinations domain mix-up: the advertised
// https://pollinations.ai is the landing site, not the API — a POST there never
// returns headers and the caller only sees the cryptic "timeout awaiting
// response headers". The OpenAI-compatible endpoint lives on
// text.pollinations.ai, so the bare domain is rewritten to it.
func normalizeProviderURL(u string) string {
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" {
		return u
	}
	switch parsed.Hostname() {
	case "pollinations.ai", "www.pollinations.ai":
		host := "text.pollinations.ai"
		if p := parsed.Port(); p != "" {
			host += ":" + p
		}
		parsed.Host = host
	}
	return parsed.String()
}

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
	if cfg.Type == OpenAIProvider {
		cfg.URL = normalizeProviderURL(cfg.URL)
	}

	httpClient := newHTTPClient()

	switch cfg.Type {
	case OpenAIProvider:
		apiPath := "/" + strings.Trim(cfg.APIPath, "/")
		if apiPath == "/" {
			apiPath = "/v1"
		}
		modelsPath := apiPath + "/models"
		if p := "/" + strings.Trim(cfg.ModelsPath, "/"); cfg.ModelsPath != "" {
			modelsPath = p
		}
		return &openAIProvider{
			url:        cfg.URL,
			host:       hostOf(cfg.URL),
			apiPath:    apiPath,
			modelsPath: modelsPath,
			model:      cfg.Model,
			apiKey:     cfg.APIKey,
			http:       httpClient,
		}
	default:
		return &ollamaProvider{
			url:   cfg.URL,
			model: cfg.Model,
			http:  httpClient,
		}
	}
}
