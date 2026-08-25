package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ollamaProvider talks to a local Ollama server (POST /api/chat, NDJSON).
type ollamaProvider struct {
	url   string
	model string
	http  *http.Client
}

func (p *ollamaProvider) Models(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.http.Do(req)
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

func (p *ollamaProvider) ChatStream(ctx context.Context, msgs []Message, onDelta func(string)) error {
	payload, err := json.Marshal(map[string]any{
		"model":    p.model,
		"messages": msgs,
		"stream":   true,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.http.Do(req)
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
