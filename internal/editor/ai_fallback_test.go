package editor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/config"
)

// pollinationsStub answers like the keyless provider: the model list under
// /models and an OpenAI-shaped stream under /openai/chat/completions.
func pollinationsStub(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"openai"},{"id":"mistral"}]}`))
		case "/openai/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestFreeFallbackWhenNothingConfigured is the "works out of the box" path: a
// fresh install whose local provider does not answer switches to the keyless
// one, and says so — the traffic leaves the machine and the user has to know.
func TestFreeFallbackWhenNothingConfigured(t *testing.T) {
	isolateHomeConfig(t)
	srv := pollinationsStub(t)

	m := New()
	m.aiFreeURLOverride = srv.URL
	m.cfg.AI.Model = ""
	if !m.cfg.AI.Unconfigured() {
		t.Fatal("precondition: a fresh model must count as unconfigured")
	}

	if !m.tryFreeFallback() {
		t.Fatal("the fallback must engage when nothing local answered")
	}
	if !m.aiFallback {
		t.Fatal("aiFallback must be recorded")
	}
	if m.chatModel != config.PollinationsPreset().Model {
		t.Fatalf("model = %q, want the keyless preset model", m.chatModel)
	}
	if m.chatNotice == "" || !strings.Contains(m.chatNotice, "leave this machine") {
		t.Fatalf("notice = %q, want a warning that data leaves the machine", m.chatNotice)
	}

	// The notice has to be visible in the chat, not just in a field.
	m.rebuildChatRows()
	var shown bool
	for _, r := range m.chatRows {
		if r.kind == "notice" && strings.Contains(r.text, "Pollinations") {
			shown = true
		}
	}
	if !shown {
		t.Fatal("the fallback notice must appear in the chat transcript")
	}

	// The provider now points at the keyless endpoint, not the dead local one.
	if got := m.aiProvider(m.chatModel); got == nil {
		t.Fatal("provider must be rebuilt for the fallback")
	}
	if !strings.Contains(m.chatNotice, "free_fallback = false") {
		t.Fatalf("notice = %q, want the config key that disables it", m.chatNotice)
	}
}

// TestFreeFallbackFiresOnlyOnce keeps a broken network from retrying the public
// service on every model pick.
func TestFreeFallbackFiresOnlyOnce(t *testing.T) {
	isolateHomeConfig(t)
	srv := pollinationsStub(t)
	m := New()
	m.aiFreeURLOverride = srv.URL

	if !m.tryFreeFallback() {
		t.Fatal("first attempt must engage")
	}
	if m.tryFreeFallback() {
		t.Fatal("the fallback must not engage twice in a session")
	}
}

// TestFreeFallbackRespectsConfiguration is the privacy rule: anything the user
// chose is never overridden by a public service.
func TestFreeFallbackRespectsConfiguration(t *testing.T) {
	isolateHomeConfig(t)
	srv := pollinationsStub(t)

	cases := map[string]func(*config.AIConfig){
		"model chosen":  func(a *config.AIConfig) { a.Model = "llama3.2" },
		"key set":       func(a *config.AIConfig) { a.APIKey = "sk-test" },
		"custom server": func(a *config.AIConfig) { a.OllamaURL = "http://10.0.0.5:11434" },
		"preset chosen": func(a *config.AIConfig) { a.Provider = config.PollinationsPreset().Name },
		"fallback off":  func(a *config.AIConfig) { a.FreeFallback = false },
	}
	for name, mutate := range cases {
		m := New()
		m.aiFreeURLOverride = srv.URL
		m.cfg.AI.Model = ""
		mutate(&m.cfg.AI)
		if m.tryFreeFallback() {
			t.Errorf("%s: the fallback must not engage", name)
		}
		if m.aiFallback {
			t.Errorf("%s: aiFallback must stay off", name)
		}
	}
}

// TestFreeFallbackNoticesUnreachableProvider: if the keyless service is down
// too, the user gets the original error rather than a silent dead panel.
func TestFreeFallbackNoticesUnreachableProvider(t *testing.T) {
	isolateHomeConfig(t)
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer dead.Close()

	m := New()
	m.aiFreeURLOverride = dead.URL
	if m.tryFreeFallback() {
		t.Fatal("a failing keyless provider must not engage the fallback")
	}
	if m.chatNotice != "" {
		t.Fatalf("notice = %q, want none when nothing works", m.chatNotice)
	}
}

// TestCycleProviderLeavesFallback: choosing a provider in the wizard is a
// decision, so the session must stop being a fallback — otherwise the "your
// code leaves this machine" notice would linger over a provider the user picked
// themselves (and the reverse: picking the keyless one on purpose would keep an
// incorrect "fallback" badge).
func TestCycleProviderLeavesFallback(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.aiFallback = true
	m.chatNotice = "cloud notice"

	m.startAISettings()
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyRight})

	if m.aiFallback {
		t.Fatal("choosing a provider must end the fallback state")
	}
	if m.chatNotice != "" {
		t.Fatalf("notice = %q, want it cleared", m.chatNotice)
	}
}
