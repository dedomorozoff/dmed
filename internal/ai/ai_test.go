package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"llama3.2:latest"},{"name":"qwen2.5-coder:7b"}]}`))
	}))
	defer srv.Close()

	p := NewProvider(Config{Type: OllamaProvider, URL: srv.URL})
	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) != 2 || models[0] != "llama3.2:latest" || models[1] != "qwen2.5-coder:7b" {
		t.Fatalf("got %v", models)
	}
}

func TestChatStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		for _, line := range []string{
			`{"message":{"role":"assistant","content":"Hel"},"done":false}`,
			`{"message":{"role":"assistant","content":"lo "},"done":false}`,
			`{"message":{"role":"assistant","content":"world"},"done":true}`,
		} {
			_, _ = w.Write([]byte(line + "\n"))
		}
	}))
	defer srv.Close()

	p := NewProvider(Config{Type: OllamaProvider, URL: srv.URL, Model: "test-model"})
	var got strings.Builder
	msgs := []Message{{Role: "user", Content: "hi"}}
	err := p.ChatStream(context.Background(), Request{Messages: msgs}, Handler{Delta: func(d string) { got.WriteString(d) }})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if got.String() != "Hello world" {
		t.Fatalf("streamed %q", got.String())
	}
}

func TestChatStreamServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"model not found"}`))
	}))
	defer srv.Close()

	p := NewProvider(Config{Type: OllamaProvider, URL: srv.URL, Model: "missing"})
	err := p.ChatStream(context.Background(), Request{}, Handler{Delta: func(string) {}})
	if err == nil || !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("want server error, got %v", err)
	}
}

func TestChatStreamInBandError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"error":"oom"}` + "\n"))
	}))
	defer srv.Close()

	p := NewProvider(Config{Type: OllamaProvider, URL: srv.URL, Model: "m"})
	err := p.ChatStream(context.Background(), Request{}, Handler{Delta: func(string) {}})
	if err == nil || !strings.Contains(err.Error(), "oom") {
		t.Fatalf("want in-band error, got %v", err)
	}
}

func TestChatStreamContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for i := 0; i < 100; i++ {
			_, _ = w.Write([]byte(`{"message":{"content":"x"},"done":false}` + "\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	p := NewProvider(Config{Type: OllamaProvider, URL: srv.URL, Model: "m"})
	err := p.ChatStream(ctx, Request{}, Handler{Delta: func(string) {}})
	if err == nil {
		t.Fatal("want context error after cancel")
	}
}

func TestDefaultURL(t *testing.T) {
	p := NewProvider(Config{Type: OllamaProvider})
	ollama := p.(*ollamaProvider)
	if ollama.url != "http://localhost:11434" {
		t.Fatalf("default url = %q", ollama.url)
	}
}

func TestOpenAIStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing Authorization header")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []string{
			`{"choices":[{"delta":{"content":"Hello"}}]}`,
			`{"choices":[{"delta":{"content":" world"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		} {
			_, _ = w.Write([]byte("data: " + chunk + "\n\n"))
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	p := NewProvider(Config{Type: OpenAIProvider, URL: srv.URL, Model: "gpt-4", APIKey: "test-key"})
	var got strings.Builder
	msgs := []Message{{Role: "user", Content: "hi"}}
	err := p.ChatStream(context.Background(), Request{Messages: msgs}, Handler{Delta: func(d string) { got.WriteString(d) }})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if got.String() != "Hello world" {
		t.Fatalf("streamed %q", got.String())
	}
}

func TestOpenAIModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4"},{"id":"gpt-3.5-turbo"}]}`))
	}))
	defer srv.Close()

	p := NewProvider(Config{Type: OpenAIProvider, URL: srv.URL})
	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) != 2 || models[0] != "gpt-4" {
		t.Fatalf("got %v", models)
	}
}

func TestOllamaToolCalling(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		dec := json.NewDecoder(r.Body)
		_ = dec.Decode(&gotBody)
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"Let me check."},"done":false}` + "\n"))
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"READ","arguments":"{\"arg\":\"a.go\"}"}}]},"done":true}` + "\n"))
	}))
	defer srv.Close()

	p := NewProvider(Config{Type: OllamaProvider, URL: srv.URL, Model: "m"})
	tools := []ToolDef{{Name: "READ", Description: "read", Parameters: map[string]any{"type": "object"}}}
	var delta strings.Builder
	var calls []ToolCall
	err := p.ChatStream(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
		Tools:    tools,
	}, Handler{Delta: func(d string) { delta.WriteString(d) }, ToolCalls: func(c []ToolCall) { calls = c }})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if delta.String() != "Let me check." {
		t.Fatalf("delta = %q", delta.String())
	}
	if len(calls) != 1 || calls[0].Name != "READ" || !strings.Contains(calls[0].Args, "a.go") {
		t.Fatalf("calls = %+v", calls)
	}
	if _, ok := gotBody["tools"]; !ok {
		t.Fatal("request missing tools array")
	}
	if msgs, ok := gotBody["messages"].([]any); !ok || len(msgs) == 0 {
		t.Fatal("request missing messages")
	}
}

func TestOpenAIToolCallingIncremental(t *testing.T) {
	var gotTools any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotTools = body["tools"]
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []string{
			`{"choices":[{"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_9","type":"function","function":{"name":"EDIT","arguments":"{\"path\":\"a.go\",\""}}]},"finish_reason":null}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"content\":\"new\"}"}}]},"finish_reason":null}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		} {
			_, _ = w.Write([]byte("data: " + chunk + "\n\n"))
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	p := NewProvider(Config{Type: OpenAIProvider, URL: srv.URL, Model: "gpt", APIKey: "k"})
	var calls []ToolCall
	err := p.ChatStream(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "edit"}},
		Tools:    []ToolDef{{Name: "EDIT", Description: "edit", Parameters: map[string]any{"type": "object"}}},
	}, Handler{ToolCalls: func(c []ToolCall) { calls = c }})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %+v", calls)
	}
	if calls[0].ID != "call_9" || calls[0].Name != "EDIT" {
		t.Fatalf("call = %+v", calls[0])
	}
	if calls[0].Args != `{"path":"a.go","content":"new"}` {
		t.Fatalf("assembled args = %q", calls[0].Args)
	}
	if gotTools == nil {
		t.Fatal("request missing tools array")
	}
}
