package config

import (
	"strings"
	"testing"
)

func TestAIPresetsCoverBasics(t *testing.T) {
	ps := AIPresets()
	if len(ps) < 5 {
		t.Fatalf("too few presets: %d", len(ps))
	}
	if ps[0].Name != "Ollama (local)" || ps[0].Kind != "ollama" || ps[0].APIKey {
		t.Fatalf("first preset must be keyless local ollama, got %+v", ps[0])
	}
	for _, p := range ps {
		switch p.Kind {
		case "ollama", "openai":
		default:
			t.Fatalf("preset %q bad kind %q", p.Name, p.Kind)
		}
		if p.BaseURL != "" && strings.HasSuffix(p.BaseURL, "/v1") {
			t.Fatalf("preset %q must use the bare origin, not a /v1 suffix", p.Name)
		}
	}
}

func TestResolvePreset(t *testing.T) {
	if got := ResolvePreset(""); got.Name != "Ollama (local)" {
		t.Fatalf("empty value must fall back to ollama, got %q", got.Name)
	}
	if got := ResolvePreset("ollama"); got.Name != "Ollama (local)" {
		t.Fatalf("legacy config value must map to a preset, got %q", got.Name)
	}
	if got := ResolvePreset("DeepSeek"); got.Name != "DeepSeek" || got.Kind != "openai" {
		t.Fatalf("case-insensitive match failed: %+v", got)
	}
	if got := ResolvePreset("no-such-thing"); got.Name != "Ollama (local)" {
		t.Fatalf("unknown value must fall back to ollama, got %q", got.Name)
	}
}
