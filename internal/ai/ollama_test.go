package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestOllamaMessagesToolArguments ensures resent assistant tool_calls carry
// arguments as a JSON object (Ollama answers 400 to string-encoded args).
func TestOllamaMessagesToolArguments(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "make a file"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{Name: "EDIT", Args: `{"path":"index.html","new":"<html></html>"}`},
		}},
		{Role: "tool", ToolName: "EDIT", Content: "[EDIT] ok"},
	}
	out := ollamaMessages(msgs)
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var parsed []struct {
		Role      string `json:"role"`
		ToolCalls []struct {
			Function struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
		ToolName string `json:"tool_name"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("payload invalid: %v\n%s", err, b)
	}
	if len(parsed[1].ToolCalls) != 1 {
		t.Fatalf("tool_calls lost: %s", b)
	}
	var args map[string]any
	if err := json.Unmarshal(parsed[1].ToolCalls[0].Function.Arguments, &args); err != nil {
		t.Fatalf("arguments must be a JSON object, got: %s", parsed[1].ToolCalls[0].Function.Arguments)
	}
	if args["path"] != "index.html" {
		t.Fatalf("unexpected args: %v", args)
	}
	if parsed[2].ToolName != "EDIT" {
		t.Fatalf("tool message must carry tool_name, got %q", parsed[2].ToolName)
	}
	// Doubly-encoded and plain-text args must not break the payload either.
	odd := ollamaMessages([]Message{{Role: "assistant", ToolCalls: []ToolCall{
		{Name: "RUN", Args: `"{\"cmd\":\"ls\"}"`},
	}}})
	bb, _ := json.Marshal(odd)
	if !strings.Contains(string(bb), `"cmd":"ls"`) {
		t.Fatalf("doubly-encoded args not unwrapped: %s", bb)
	}
}
