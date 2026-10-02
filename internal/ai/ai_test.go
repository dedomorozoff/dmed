package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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

// TestClientHasNoOverallTimeout pins the streaming contract: an overall
// http.Client.Timeout would abort a long generation mid-answer. The bound is
// on connecting and on waiting for response headers instead.
func TestClientHasNoOverallTimeout(t *testing.T) {
	tr, ok := newHTTPClient().Transport.(*http.Transport)
	if !ok {
		t.Fatal("client must use an explicit *http.Transport")
	}
	if tr.ResponseHeaderTimeout <= 0 {
		t.Error("ResponseHeaderTimeout must be set: a dead server must not pin a chat forever")
	}
	if tr.DialContext == nil {
		t.Error("DialContext with a timeout must be set")
	}
}

// TestChatStreamOpenAIContextCancel verifies the same cancellation contract for
// the OpenAI-compatible wire format (SSE).
func TestChatStreamOpenAIContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f, _ := w.(http.Flusher)
		for i := 0; i < 100; i++ {
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n"))
			if f != nil {
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
	p := NewProvider(Config{Type: OpenAIProvider, URL: srv.URL, Model: "m"})
	if err := p.ChatStream(ctx, Request{}, Handler{Delta: func(string) {}}); err == nil {
		t.Fatal("want context error after cancel")
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

// TestOpenAICustomAPIPath covers providers that are OpenAI-compatible but do not
// live under /v1 — Pollinations serves the same protocol under /openai and
// lists its models at /models, and hardcoding the prefix would make the
// no-signup provider unusable.
// TestOpenAIKeylessStillSendsAuth pins the Pollinations quirk: the endpoint
// rejects a request that carries no Authorization header at all (answering with
// an error instead of a completion) even though it ignores the value, so a
// keyless provider must send a placeholder Bearer token.
// TestNormalizeProviderURL pins the Pollinations domain fix: the bare
// https://pollinations.ai is the landing page, and a POST there hangs until the
// header timeout fires with no hint of what is wrong. The API lives on
// text.pollinations.ai, so the advertised domain must be rewritten; every other
// URL passes through untouched.
// TestOpenAIHostGateSerializesRequests pins the keyless-tier guard: the
// free Pollinations tier allows a single in-flight request per IP and rejects
// the second one with a 429, so concurrent features (chat, ghost text, agent)
// must queue on the host instead of racing.
func TestOpenAIHostGateSerializesRequests(t *testing.T) {
	var cur, max int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&cur, 1)
		for {
			old := atomic.LoadInt32(&max)
			if n <= old || atomic.CompareAndSwapInt32(&max, old, n) {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
		atomic.AddInt32(&cur, -1)
		_, _ = w.Write([]byte(`{"data":[{"id":"m"}]}`))
	}))
	defer srv.Close()

	p := NewProvider(Config{Type: OpenAIProvider, URL: srv.URL, APIPath: "/v1"})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := p.Models(ctx); err != nil {
				t.Errorf("Models: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&max); got != 1 {
		t.Fatalf("max concurrent requests to one host = %d, want 1", got)
	}
}

// TestNormalizeProviderURL pins the Pollinations domain fix: the bare
func TestNormalizeProviderURL(t *testing.T) {
	cases := map[string]string{
		"https://pollinations.ai":       "https://text.pollinations.ai",
		"https://www.pollinations.ai":   "https://text.pollinations.ai",
		"https://text.pollinations.ai":  "https://text.pollinations.ai",
		"https://api.openai.com":        "https://api.openai.com",
		"http://localhost:1234":         "http://localhost:1234",
		"https://api.deepseek.com":      "https://api.deepseek.com",
		"https://pollinations.ai:8443/": "https://text.pollinations.ai:8443/",
		"not a url at all":              "not a url at all",
	}
	for in, want := range cases {
		if got := normalizeProviderURL(in); got != want {
			t.Errorf("normalizeProviderURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestOpenAIKeylessStillSendsAuth pins the Pollinations quirk: the endpoint
func TestOpenAIKeylessStillSendsAuth(t *testing.T) {
	var authModels, authChat string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/openai/chat/completions":
			authChat = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		case "/models":
			authModels = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{"data":[{"id":"openai"}]}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	p := NewProvider(Config{Type: OpenAIProvider, URL: srv.URL, Model: "openai",
		APIPath: "/openai", ModelsPath: "/models"})

	if _, err := p.Models(context.Background()); err != nil {
		t.Fatalf("Models: %v", err)
	}
	if authModels == "" {
		t.Error("the model list request must carry an Authorization header even without a key")
	}
	if err := p.ChatStream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}},
		Handler{Delta: func(string) {}}); err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if authChat == "" {
		t.Error("the chat request must carry an Authorization header even without a key")
	}
}

// TestOpenAICustomAPIPath covers providers that are OpenAI-compatible but do not
func TestOpenAICustomAPIPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/openai/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		case "/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"openai"},{"id":"mistral"}]}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	p := NewProvider(Config{Type: OpenAIProvider, URL: srv.URL, Model: "openai",
		APIPath: "/openai", ModelsPath: "/models"})

	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) != 2 || models[0] != "openai" {
		t.Fatalf("models = %v", models)
	}

	var got strings.Builder
	if err := p.ChatStream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}},
		Handler{Delta: func(d string) { got.WriteString(d) }}); err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if got.String() != "hi" {
		t.Fatalf("streamed %q", got.String())
	}
}

// TestOpenAIModelListShapes covers the endpoints that claim to be
// OpenAI-compatible and then answer with something else. Pollinations — the
// keyless provider — returns a bare array of objects with "name", not the
// {"data":[{"id":...}]} envelope; parsing only the envelope left it with an empty
// list, which looks exactly like "this server has no models".
func TestOpenAIModelListShapes(t *testing.T) {
	cases := map[string]struct {
		body string
		want []string
	}{
		"openai envelope": {`{"data":[{"id":"gpt-4o-mini"},{"id":"gpt-4o"}]}`, []string{"gpt-4o-mini", "gpt-4o"}},
		"pollinations":    {`[{"name":"openai-fast","aliases":["openai"]},{"name":"mistral"}]`, []string{"openai-fast", "mistral"}},
		"array of names":  {`["openai","mistral","searchgpt"]`, []string{"openai", "mistral", "searchgpt"}},
		"envelope empty":  {`{"data":[]}`, []string{}},
	}
	for name, c := range cases {
		body := c.body
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/models" {
				t.Errorf("%s: unexpected path %q", name, r.URL.Path)
			}
			_, _ = w.Write([]byte(body))
		}))

		p := NewProvider(Config{Type: OpenAIProvider, URL: srv.URL, APIPath: "/openai", ModelsPath: "/models"})
		got, err := p.Models(context.Background())
		srv.Close()
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("%s: models = %v, want %v", name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: models = %v, want %v", name, got, c.want)
				break
			}
		}
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

func TestChatStreamSendsOptions(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"hi"},"done":true}` + "\n"))
	}))
	defer srv.Close()

	p := NewProvider(Config{Type: OllamaProvider, URL: srv.URL, Model: "m"})
	err := p.ChatStream(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "x"}},
		Options:  Options{Temperature: 8, NumCtx: 32768, NumPredict: 512},
	}, Handler{Delta: func(string) {}})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if _, ok := gotBody["temperature"]; !ok {
		t.Fatalf("request missing temperature: %v", gotBody)
	}
	if opt, ok := gotBody["options"].(map[string]any); !ok {
		t.Fatalf("request missing options: %v", gotBody)
	} else if opt["num_ctx"].(float64) != 32768 || opt["num_predict"].(float64) != 512 {
		t.Fatalf("options = %v, want num_ctx 32768 / num_predict 512", opt)
	}
}

func TestChatStreamOmitsOptionsWhenUnset(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"hi"},"done":true}` + "\n"))
	}))
	defer srv.Close()

	p := NewProvider(Config{Type: OllamaProvider, URL: srv.URL, Model: "m"})
	err := p.ChatStream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "x"}}}, Handler{Delta: func(string) {}})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if _, ok := gotBody["options"]; ok {
		t.Fatalf("options should be omitted when all zero: %v", gotBody)
	}
	if _, ok := gotBody["temperature"]; ok {
		t.Fatalf("temperature should be omitted when zero: %v", gotBody)
	}
}
