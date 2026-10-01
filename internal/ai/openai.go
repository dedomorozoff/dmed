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
	"sync"
)

// openAIProvider talks to an OpenAI-compatible API (POST <apiPath>/chat/completions,
// SSE). Compatible with: OpenAI, DeepSeek, Groq, Together, vLLM, LM Studio,
// Unsloth, Pollinations, etc. apiPath exists because "OpenAI-compatible" does
// not always mean "/v1": Pollinations serves the same JSON under /openai.
type openAIProvider struct {
	url        string
	host       string // for the per-host request gate
	apiPath    string // e.g. "/v1" or "/openai"
	modelsPath string // e.g. "/v1/models" or "/models"
	model      string
	apiKey     string
	http       *http.Client
}

// The keyless cloud tiers are strictly one-request-at-a-time per IP:
// Pollinations answers a second concurrent request with a 429 ("Queue full for
// IP"), and nothing in a single-user editor gains from pipelining chat, ghost
// text and agents to the same host anyway. Requests to one host therefore
// queue on a gate instead of failing; ctx cancellation (Esc) still cuts the
// wait short.
var (
	hostGatesMu sync.Mutex
	hostGates   = map[string]chan struct{}{}
)

func gateFor(host string) chan struct{} {
	hostGatesMu.Lock()
	defer hostGatesMu.Unlock()
	g, ok := hostGates[host]
	if !ok {
		g = make(chan struct{}, 1)
		hostGates[host] = g
	}
	return g
}

// acquireGate takes the host's single slot, or gives up when ctx ends first.
func acquireGate(ctx context.Context, g chan struct{}) error {
	select {
	case g <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// modelList covers the three shapes a "list your models" endpoint comes in:
// the OpenAI one ({"data":[{"id":...}]}), a bare array of objects (Pollinations
// returns [{"name":...}]) and a bare array of names. The last two are not
// OpenAI-compatible, but refusing them would leave the keyless provider with an
// empty list and no way to pick a model — a failure that looks like "the server
// has nothing", which is exactly the confusion this code exists to remove.
func (o *openAIProvider) modelList(data []byte) ([]string, error) {
	var envelope struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err == nil && len(envelope.Data) > 0 {
		names := make([]string, 0, len(envelope.Data))
		for _, m := range envelope.Data {
			if n := firstNonEmpty(m.ID, m.Name); n != "" {
				names = append(names, n)
			}
		}
		return names, nil
	}
	var objects []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &objects); err == nil && len(objects) > 0 {
		names := make([]string, 0, len(objects))
		for _, m := range objects {
			if n := firstNonEmpty(m.ID, m.Name); n != "" {
				names = append(names, n)
			}
		}
		return names, nil
	}
	var plain []string
	if err := json.Unmarshal(data, &plain); err == nil && len(plain) > 0 {
		out := make([]string, 0, len(plain))
		for _, n := range plain {
			if n = strings.TrimSpace(n); n != "" {
				out = append(out, n)
			}
		}
		return out, nil
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	// A well-formed but empty list is a valid answer, not an error.
	return []string{}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func (p *openAIProvider) Models(ctx context.Context) ([]string, error) {
	g := gateFor(p.host)
	if err := acquireGate(ctx, g); err != nil {
		return nil, err
	}
	defer func() { <-g }()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url+p.modelsPath, nil)
	if err != nil {
		return nil, err
	}
	p.setAuth(req)
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("openai %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxModelList))
	if err != nil {
		return nil, err
	}
	return p.modelList(data)
}

// maxModelList caps the model list: a local Ollama can report dozens, and the
// answer only has to be readable by a person choosing from it.
const maxModelList = 1 << 20 // 1 MiB

// guestBearer is sent when no API key is configured. Some keyless endpoints
// (Pollinations) ignore the value but reject a request that carries no
// Authorization header at all — answering it with an error instead of the
// completion — so a placeholder always goes out. Servers that do not care
// about the header simply ignore it.
const guestBearer = "guest"

func (p *openAIProvider) setAuth(r *http.Request) {
	if p.apiKey != "" {
		r.Header.Set("Authorization", "Bearer "+p.apiKey)
		return
	}
	r.Header.Set("Authorization", "Bearer "+guestBearer)
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
	if req.Options.Temperature > 0 {
		body["temperature"] = float32(req.Options.Temperature) / 10
	}
	if req.Options.NumPredict > 0 {
		body["max_tokens"] = req.Options.NumPredict
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	g := gateFor(p.host)
	if err := acquireGate(ctx, g); err != nil {
		return err
	}
	defer func() { <-g }()
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url+p.apiPath+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	p.setAuth(r)
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
		// See ollama.ChatStream: the stream body has no deadline, so ctx
		// cancellation is what makes Esc stop a generation promptly.
		if err := ctx.Err(); err != nil {
			return err
		}
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
