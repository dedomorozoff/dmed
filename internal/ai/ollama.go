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

func (p *ollamaProvider) ChatStream(ctx context.Context, req Request, h Handler) error {
	body := map[string]any{
		"model":    p.model,
		"messages": ollamaMessages(req.Messages),
		"stream":   true,
	}
	if len(req.Tools) > 0 {
		body["tools"] = ollamaTools(req.Tools)
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := p.http.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("ollama %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	toolFired := false
	for sc.Scan() {
		var chunk struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Function struct {
						Name      string          `json:"name"`
						Arguments json.RawMessage `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
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
		if chunk.Message.Content != "" && h.Delta != nil {
			h.Delta(chunk.Message.Content)
		}
		// Tool calls usually arrive in their own chunk before the done marker,
		// so collect them on any chunk. Some providers deliver them together
		// with done instead; fire at most once per response.
		if len(chunk.Message.ToolCalls) > 0 && h.ToolCalls != nil && !toolFired {
			toolFired = true
			calls := make([]ToolCall, 0, len(chunk.Message.ToolCalls))
			for i, tc := range chunk.Message.ToolCalls {
				calls = append(calls, ToolCall{
					ID:   fmt.Sprintf("call_%d", i),
					Name: tc.Function.Name,
					Args: ollamaArgsString(tc.Function.Arguments),
				})
			}
			h.ToolCalls(calls)
		}
		if chunk.Done {
			return nil
		}
	}
	return sc.Err()
}

// ollamaArgsString normalizes tool-call arguments: Ollama sends them as a JSON
// object, but some models emit a JSON-encoded string.
func ollamaArgsString(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "{}"
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
	}
	return trimmed
}

// ollamaMessages maps ai.Message to the Ollama /api/chat wire format. Assistant
// tool-call messages carry tool_calls; role "tool" messages use tool_name.
func ollamaMessages(msgs []Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "tool" {
			out = append(out, map[string]any{
				"role":      "tool",
				"content":   m.Content,
				"tool_name": m.ToolName,
			})
			continue
		}
		mm := map[string]any{"role": m.Role, "content": m.Content}
		if len(m.ToolCalls) > 0 {
			tcs := make([]map[string]any, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				tcs = append(tcs, map[string]any{
					"function": map[string]any{
						"name":      tc.Name,
						"arguments": tc.Args,
					},
				})
			}
			mm["tool_calls"] = tcs
		}
		out = append(out, mm)
	}
	return out
}

// ollamaTools maps ToolDef to the Ollama tools array format.
func ollamaTools(tools []ToolDef) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.Parameters,
			},
		})
	}
	return out
}
