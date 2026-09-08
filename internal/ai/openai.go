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

// openAIProvider talks to an OpenAI-compatible API (POST /v1/chat/completions, SSE).
// Compatible with: OpenAI, DeepSeek, Groq, Together, vLLM, LM Studio, etc.
type openAIProvider struct {
	url    string
	model  string
	apiKey string
	http   *http.Client
}

func (p *openAIProvider) Models(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("openai %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var res struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(res.Data))
	for _, m := range res.Data {
		names = append(names, m.ID)
	}
	return names, nil
}

func (p *openAIProvider) ChatStream(ctx context.Context, req Request, h Handler) error {
	body := map[string]any{
		"model":    p.model,
		"messages": openAIMessages(req.Messages),
		"stream":   true,
	}
	if len(req.Tools) > 0 {
		body["tools"] = openAITools(req.Tools)
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		r.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	resp, err := p.http.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("openai %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}

	// Tool calls stream incrementally: each chunk carries a partial index,
	// and the id/name/arguments pieces must be assembled in order.
	type accTool struct {
		id   string
		name string
		args strings.Builder
	}
	acc := map[int]*accTool{}
	var order []int

	flushTools := func() {
		if h.ToolCalls == nil {
			return
		}
		calls := make([]ToolCall, 0, len(order))
		for _, idx := range order {
			a := acc[idx]
			if a == nil || a.name == "" {
				continue
			}
			calls = append(calls, ToolCall{ID: a.id, Name: a.name, Args: a.args.String()})
		}
		if len(calls) > 0 {
			h.ToolCalls(calls)
		}
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		// SSE format: "data: {...}" or "data: [DONE]"
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			flushTools()
			return nil
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue // skip malformed lines
		}
		if chunk.Error != nil {
			return fmt.Errorf("openai: %s", chunk.Error.Message)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		d := chunk.Choices[0].Delta
		if d.Content != "" && h.Delta != nil {
			h.Delta(d.Content)
		}
		for _, tc := range d.ToolCalls {
			a, ok := acc[tc.Index]
			if !ok {
				a = &accTool{}
				acc[tc.Index] = a
				order = append(order, tc.Index)
			}
			if tc.ID != "" {
				a.id = tc.ID
			}
			if tc.Function.Name != "" {
				a.name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				a.args.WriteString(tc.Function.Arguments)
			}
		}
		if chunk.Choices[0].FinishReason != nil {
			flushTools()
			return nil
		}
	}
	return sc.Err()
}

// openAIMsg is the wire form of a single message for /v1/chat/completions.
type openAIMsg struct {
	Role       string           `json:"role"`
	Content    *string          `json:"content"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAIToolCall struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// openAIMessages maps ai.Message to the OpenAI wire format. Assistant
// messages with tool_calls use a null content; role "tool" messages use the
// matching tool_call_id.
func openAIMessages(msgs []Message) []openAIMsg {
	out := make([]openAIMsg, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "tool" {
			out = append(out, openAIMsg{Role: "tool", ToolCallID: m.ToolCallID, Content: strPtr(m.Content)})
			continue
		}
		var content *string
		if len(m.ToolCalls) > 0 {
			content = nil // OpenAI requires null content when tool_calls present
		} else {
			content = strPtr(m.Content)
		}
		o := openAIMsg{Role: m.Role, Content: content}
		if len(m.ToolCalls) > 0 {
			tcs := make([]openAIToolCall, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				tcs = append(tcs, openAIToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: openAIFunction{
						Name:      tc.Name,
						Arguments: tc.Args,
					},
				})
			}
			o.ToolCalls = tcs
		}
		out = append(out, o)
	}
	return out
}

func strPtr(s string) *string { return &s }

// openAITools maps ToolDef to the OpenAI tools array format.
func openAITools(tools []ToolDef) []map[string]any {
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
