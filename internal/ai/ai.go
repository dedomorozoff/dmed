// Package ai provides clients for LLM backends. The first target is a
// local Ollama server (POST /api/chat with NDJSON streaming), which keeps
// the editor free of paid API dependencies.
package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// Message is one chat turn.
type Message struct {
	Role    string `json:"role"` // "system" | "user" | "assistant"
	Content string `json:"content"`
}

// Client talks to an Ollama-compatible server.
type Client struct {
	URL   string // base URL, e.g. http://localhost:11434
	Model string // model tag, e.g. "qwen2.5-coder:7b"; empty = caller picks
	HTTP  *http.Client
}

// NewClient builds a client; an empty url falls back to the local default.
func NewClient(url, model string) *Client {
	if url == "" {
		url = "http://localhost:11434"
	}
	return &Client{
		URL:   strings.TrimRight(url, "/"),
		Model: model,
		HTTP:  &http.Client{},
	}
}

// DefaultClient builds a client from DMED_OLLAMA_URL / DMED_MODEL.
func DefaultClient() *Client {
	return NewClient(os.Getenv("DMED_OLLAMA_URL"), os.Getenv("DMED_MODEL"))
}

// Models lists installed model tags via /api/tags.
func (c *Client) Models(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("ollama %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var res struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(res.Models))
	for _, mm := range res.Models {
		names = append(names, mm.Name)
	}
	return names, nil
}

// ChatStream sends the conversation and calls onDelta for every streamed
// content chunk. It returns when the server reports done or the context is
// cancelled. A non-nil error means the exchange failed midway.
func (c *Client) ChatStream(ctx context.Context, msgs []Message, onDelta func(string)) error {
	payload, err := json.Marshal(map[string]any{
		"model":    c.Model,
		"messages": msgs,
		"stream":   true,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("ollama %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var chunk struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Error string `json:"error"`
			Done  bool   `json:"done"`
		}
		if err := json.Unmarshal(sc.Bytes(), &chunk); err != nil {
			continue // skip malformed keep-alive lines
		}
		if chunk.Error != "" {
			return fmt.Errorf("ollama: %s", chunk.Error)
		}
		if chunk.Message.Content != "" && onDelta != nil {
			onDelta(chunk.Message.Content)
		}
		if chunk.Done {
			return nil
		}
	}
	return sc.Err()
}
