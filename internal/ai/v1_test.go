package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestOpenAIV1SuffixStripped verifies that a base URL pasted with a /v1 suffix
// (the form many providers advertise) still works: the provider appends the
// API path itself, so the suffix must be stripped, not doubled.
func TestOpenAIV1SuffixStripped(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"m1"}]}`))
	}))
	defer srv.Close()

	p := NewProvider(Config{Type: OpenAIProvider, URL: srv.URL + "/v1", APIKey: "k"})
	if _, err := p.Models(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/models" {
		t.Fatalf("path = %q, want /v1/models", gotPath)
	}
}
