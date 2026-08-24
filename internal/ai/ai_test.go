package ai

import (
	"context"
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

	c := NewClient(srv.URL, "")
	models, err := c.Models(context.Background())
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

	c := NewClient(srv.URL, "test-model")
	var got strings.Builder
	msgs := []Message{{Role: "user", Content: "hi"}}
	err := c.ChatStream(context.Background(), msgs, func(d string) { got.WriteString(d) })
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

	c := NewClient(srv.URL, "missing")
	err := c.ChatStream(context.Background(), nil, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("want server error, got %v", err)
	}
}

func TestChatStreamInBandError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"error":"oom"}` + "\n"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "m")
	err := c.ChatStream(context.Background(), nil, func(string) {})
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
	c := NewClient(srv.URL, "m")
	err := c.ChatStream(ctx, nil, func(string) {})
	if err == nil {
		t.Fatal("want context error after cancel")
	}
}

func TestDefaultURL(t *testing.T) {
	c := NewClient("", "")
	if c.URL != "http://localhost:11434" {
		t.Fatalf("default url = %q", c.URL)
	}
}
