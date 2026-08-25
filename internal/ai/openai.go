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

func (p *openAIProvider) ChatStream(ctx context.Context, msgs []Message, onDelta func(string)) error {
	type openAIMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	oMsgs := make([]openAIMsg, len(msgs))
	for i, m := range msgs {
		oMsgs[i] = openAIMsg{Role: m.Role, Content: m.Content}
	}

	payload, err := json.Marshal(map[string]any{
		"model":    p.model,
		"messages": oMsgs,
		"stream":   true,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("openai %s: %s", resp.Status, strings.TrimSpace(string(body)))
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
			return nil
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
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
		if len(chunk.Choices) > 0 {
			if chunk.Choices[0].Delta.Content != "" && onDelta != nil {
				onDelta(chunk.Choices[0].Delta.Content)
			}
			if chunk.Choices[0].FinishReason != nil {
				return nil
			}
		}
	}
	return sc.Err()
}
