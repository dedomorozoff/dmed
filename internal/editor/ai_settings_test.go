package editor

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// isolateHomeConfig points the user home dir at a temp dir so the tests run
// against default configuration instead of whatever ~/.dmed.conf exists on
// the machine (config.Load reads os.UserHomeDir, which on Windows is
// USERPROFILE and on Unix is HOME — set both for cross-platform coverage).
func isolateHomeConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("USERPROFILE", dir)
	t.Setenv("HOME", dir)
}

// pickAISelect drives the option list the way a user does: Enter to open, ↑/↓
// until the wanted option is highlighted, Enter to pick.
func pickAISelect(t *testing.T, m *Model, field int, name string) tea.Cmd {
	t.Helper()
	m.aiCfgField = field
	if !m.openAISelect(field) {
		t.Fatalf("row %d offers no options", field)
	}
	for i := 0; i <= len(m.aiCfgSelItems); i++ {
		if m.aiCfgSelItems[m.aiCfgSelIdx] == name {
			break
		}
		m.handleAISelectKey(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if got := m.aiCfgSelItems[m.aiCfgSelIdx]; got != name {
		t.Fatalf("option %q is not in the list %v", name, m.aiCfgSelItems)
	}
	if handled, changed := m.handleAISelectKey(tea.KeyPressMsg{Code: tea.KeyEnter}); !handled {
		t.Fatal("Enter on the highlighted option must be handled by the list")
	} else if changed {
		return m.afterChoiceChange()
	}
	return nil
}

// TestAISettingsProviderSelect covers the provider row as a list rather than an
// arrow-cycler: nine presets and a fifty-entry model list cannot be walked one
// arrow press at a time.
func TestAISettingsProviderSelect(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.startAISettings()
	if !m.aiCfgOpen {
		t.Fatal("wizard should open")
	}
	if m.cfg.AI.Provider != "Ollama (local)" {
		t.Fatalf("prov in default = %q", m.cfg.AI.Provider)
	}

	// Enter opens the list, with the current value highlighted.
	m.aiCfgField = 0
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.aiCfgSelOpen {
		t.Fatal("Enter on the Provider row must open the list")
	}
	if m.aiCfgSelItems[m.aiCfgSelIdx] != "Ollama (local)" {
		t.Fatalf("the current value must be highlighted, got %q", m.aiCfgSelItems[m.aiCfgSelIdx])
	}
	// Arrows must not silently change the value while the list is open.
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.cfg.AI.Provider != "Ollama (local)" {
		t.Fatalf("the value changed before a pick: %q", m.cfg.AI.Provider)
	}
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyUp}) // no-op at the top
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyDown})
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.aiCfgSelOpen {
		t.Fatal("the list must close after a pick")
	}
	if m.cfg.AI.Provider != "Pollinations (free, no key)" {
		t.Fatalf("after pick = %q, want the keyless preset", m.cfg.AI.Provider)
	}
	// The keyless preset must carry its own endpoint prefix, or the probe would
	// hit /v1 and report a 404 as "auth failed".
	if m.cfg.AI.APIPath != "/openai" || m.cfg.AI.ModelsPath != "/models" {
		t.Fatalf("api paths = %q / %q", m.cfg.AI.APIPath, m.cfg.AI.ModelsPath)
	}
	// The open-wizard probe is still in flight here, so the reload for the new
	// provider is queued rather than started; TestAISettingsReloadsAfterDeferredPick
	// covers that path.
	if !m.aiCfgNeedsReload && !m.aiCfgTest.running {
		t.Fatal("a new provider must trigger a model reload")
	}

	// A /v1 provider must clear the prefix again.
	pickAISelect(t, &m, 0, "OpenAI")
	if m.cfg.AI.OllamaURL != "https://api.openai.com" {
		t.Fatalf("url after pick = %q, want the preset URL", m.cfg.AI.OllamaURL)
	}
	if m.cfg.AI.APIPath != "" {
		t.Fatalf("api path must reset for a /v1 provider, got %q", m.cfg.AI.APIPath)
	}
}

func TestAISettingsSelectEscapeChangesNothing(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.startAISettings()
	before := m.cfg.AI.Provider

	m.aiCfgField = 0
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyDown})
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEsc})

	if m.aiCfgSelOpen {
		t.Fatal("Esc must close the list")
	}
	if m.cfg.AI.Provider != before {
		t.Fatalf("Esc changed the provider: %q -> %q", before, m.cfg.AI.Provider)
	}
	// Esc on the row still closes the wizard.
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.aiCfgOpen {
		t.Fatal("Esc must close the wizard")
	}
}

// TestAISettingsModelSelectTypeManually keeps the escape hatch: a model the
// server does not report must still be typeable.
func TestAISettingsModelSelectTypeManually(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.startAISettings()
	m.handleAITestResult(AITestResultMsg{OK: true, Status: "2 models", Gen: m.aiCfgGen,
		Models: []string{"a-model", "b-model"}})

	m.aiCfgField = 1
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.aiCfgSelOpen {
		t.Fatal("Enter on the Model row must open the list")
	}
	if len(m.aiCfgSelItems) != 3 || m.aiCfgSelItems[2] != aiSelectTypeItem {
		t.Fatalf("items = %v, want the models plus a type-a-name entry", m.aiCfgSelItems)
	}

	// Walk to the type-a-name entry and pick it.
	for m.aiCfgSelItems[m.aiCfgSelIdx] != aiSelectTypeItem {
		m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.aiCfgEdit {
		t.Fatal("picking the type-a-name entry must open inline editing")
	}
	for _, r := range []rune("my-private-fork") {
		m.handleAISettings(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.cfg.AI.Model != "my-private-fork" {
		t.Fatalf("model = %q, want the typed name", m.cfg.AI.Model)
	}
}

// TestAISettingsSelectsForFlagRows covers Allow Run and Restrict Root, which were
// rendered blank and accepted no arrow keys before.
func TestAISettingsSelectsForFlagRows(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.startAISettings()

	if m.aiFieldValue(9) != "ask" {
		t.Fatalf("Allow Run shows %q, want the shipped default", m.aiFieldValue(9))
	}
	pickAISelect(t, &m, 9, "always")
	if m.cfg.AI.AllowRun != "always" {
		t.Fatalf("allow_run = %q", m.cfg.AI.AllowRun)
	}
	if m.aiFieldValue(9) != "always" {
		t.Fatalf("the row must show the new value, got %q", m.aiFieldValue(9))
	}

	if m.aiFieldValue(10) != "true" {
		t.Fatalf("Restrict Root shows %q, want true by default", m.aiFieldValue(10))
	}
	pickAISelect(t, &m, 10, "false")
	if m.cfg.AI.RestrictToRoot {
		t.Fatal("Restrict Root must be off after picking false")
	}
}

// TestAISettingsEveryRowHasAValue is the regression test for the blank rows: a
// field with no case in aiFieldValue renders empty and silently ignores edits.
func TestAISettingsEveryRowHasAValue(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.cfg.AI.Model = "some-model"
	m.cfg.AI.OllamaURL = "http://localhost:11434"
	m.cfg.AI.APIKey = "secret"
	m.startAISettings()
	m.handleAITestResult(AITestResultMsg{OK: true, Status: "1 models", Gen: m.aiCfgGen,
		Models: []string{"some-model"}})

	for i, f := range aiSettingsFields {
		if f.kind == "action" {
			continue
		}
		if v := m.aiFieldValue(i); strings.TrimSpace(v) == "" {
			t.Errorf("row %d (%s) renders blank", i, f.name)
		}
	}
}

// TestAISettingsNumericRowsCommit covers Temperature / Num Ctx / Num Predict /
// Tool Rounds, which accepted Enter and typing and then threw the value away.
func TestAISettingsNumericRowsCommit(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.startAISettings()

	type numRow struct {
		field int
		get   func() int
		want  int
	}
	rows := []numRow{
		{5, func() int { return m.cfg.AI.Temperature }, 8},
		{6, func() int { return m.cfg.AI.NumCtx }, 32768},
		{7, func() int { return m.cfg.AI.NumPredict }, 512},
		{8, func() int { return m.cfg.AI.ToolRounds }, 3},
	}
	for _, r := range rows {
		m.aiCfgField = r.field
		m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEnter})
		if !m.aiCfgEdit {
			t.Fatalf("row %d must open inline editing", r.field)
		}
		for _, ch := range strconv.Itoa(r.want) {
			m.handleAISettings(tea.KeyPressMsg{Code: ch, Text: string(ch)})
		}
		m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEnter})
		if got := r.get(); got != r.want {
			t.Errorf("row %d = %d, want %d", r.field, got, r.want)
		}
	}

	// A non-numeric entry must not zero a working value.
	m.aiCfgField = 5
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.handleAISettings(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.cfg.AI.Temperature != 8 {
		t.Fatalf("a rejected entry changed the value: %d", m.cfg.AI.Temperature)
	}
}

// TestAISettingsSelectRendersUnderRow checks the list is actually drawn and that
// a long model list scrolls instead of pushing the panel off screen.
func TestAISettingsSelectRendersUnderRow(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.startAISettings()
	var models []string
	for i := 0; i < 20; i++ {
		models = append(models, "model-"+strconv.Itoa(i))
	}
	m.handleAITestResult(AITestResultMsg{OK: true, Status: "20 models", Gen: m.aiCfgGen, Models: models})

	m.aiCfgField = 1
	m.openAISelect(1)
	for i := 0; i < 12; i++ {
		m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	rows := m.aiSettingsPanel(30)
	joined := strings.Join(rows, "\n")
	if !containsStr(joined, "> model-12") {
		t.Fatalf("the highlighted option must be drawn: %q", joined)
	}
	if !containsStr(joined, "more") {
		t.Fatalf("a truncated list must say so: %q", joined)
	}
	// The window stays small: twenty models must not render twenty rows.
	if n := len(m.aiSelectRows()); n > 10 {
		t.Fatalf("select drew %d rows, want a windowed list", n)
	}
	// Going down past the end stays put on the last entry.
	for i := 0; i < 30; i++ {
		m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if m.aiCfgSelItems[m.aiCfgSelIdx] != aiSelectTypeItem {
		t.Fatalf("the selection ran past the end: %q", m.aiCfgSelItems[m.aiCfgSelIdx])
	}
}

// TestAISettingsLegacyProviderNormalizes verifies that a config written by an
// older dmed (provider = ollama) still lands on the matching preset.
func TestAISettingsLegacyProviderNormalizes(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.cfg.AI.Provider = "openai"
	m.startAISettings()
	if m.cfg.AI.Provider != "OpenAI" {
		t.Fatalf("legacy openai not normalized, got %q", m.cfg.AI.Provider)
	}
}

// TestAISettingsTestRowConnectionProbe verifies the Test row: 't' (or Enter)
// starts a probe, and the result message flips the status to the human hint. The
// wizard already fires one probe on open, so the test settles that first.
func TestAISettingsTestRowConnectionProbe(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.startAISettings()
	m.handleAITestResult(AITestResultMsg{OK: true, Status: "1 models", Gen: m.aiCfgGen,
		Models: []string{"first-model"}})

	m.aiCfgField = len(aiSettingsFields) - 1 // Test row
	cmd := m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || !m.aiCfgTest.running {
		t.Fatal("Enter on the Test row must start a probe when none is running")
	}
	m.handleAITestResult(AITestResultMsg{OK: true, Status: "2 models", Gen: m.aiCfgGen,
		Models: []string{"first-model", "second-model"}})
	if !m.aiCfgTest.ok || m.aiCfgTest.running {
		t.Fatalf("state after ok result: %+v", m.aiCfgTest)
	}
	if len(m.aiCfgModels) != 2 {
		t.Fatalf("the probe must refresh the model list, got %v", m.aiCfgModels)
	}

	m.aiCfgField = 0 // 't' works from any row
	cmd = m.handleAISettings(tea.KeyPressMsg{Code: 't'})
	if cmd == nil {
		t.Fatal("t should start a probe when none is running")
	}
	m.handleAITestResult(AITestResultMsg{OK: false, Status: "dial tcp: connection refused", Gen: m.aiCfgGen})
	if m.aiCfgTest.ok || strings.Contains(m.aiCfgTest.status, "dial tcp") {
		t.Fatalf("refused must be translated into a human hint, got %q", m.aiCfgTest.status)
	}
}

// TestAISettingsReloadsAfterDeferredPick covers the race the probe flag exists
// for: the user picks a new provider while a probe is in flight, so the reload
// cannot start immediately and must run when the in-flight reply lands.
func TestAISettingsReloadsAfterDeferredPick(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.startAISettings() // probe in flight

	pickAISelect(t, &m, 0, "Groq")
	if m.cfg.AI.Provider != "Groq" {
		t.Fatalf("the pick itself must apply immediately, got %q", m.cfg.AI.Provider)
	}
	if !m.aiCfgNeedsReload {
		t.Fatal("a pick during a probe must remember that a reload is due")
	}

	// The old reply lands; it belongs to the previous endpoint, so it is dropped.
	gen := m.aiCfgGen
	m.handleAITestResult(AITestResultMsg{OK: true, Status: "1 models", Gen: gen - 1,
		Models: []string{"stale-model"}})
	if len(m.aiCfgModels) != 0 {
		t.Fatalf("a stale reply must not fill the list: %v", m.aiCfgModels)
	}

	// The current request answers, and the deferred reload starts right after.
	cmd := m.handleAITestResult(AITestResultMsg{OK: true, Status: "1 models", Gen: gen,
		Models: []string{"groq-model"}})
	if cmd == nil {
		t.Fatal("the deferred reload must fire once the probe finishes")
	}
	if m.aiCfgNeedsReload {
		t.Fatal("the deferred reload flag must be cleared")
	}
}

// TestAISettingsIgnoresStaleProbe: a reply from the endpoint the user already
// navigated away from must not repopulate the Model row.
func TestAISettingsIgnoresStaleProbe(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.startAISettings()
	stale := m.aiCfgGen

	// The user changes the provider while the probe is in flight.
	m.cycleAIProvider(1)
	if m.aiCfgGen == stale {
		t.Fatal("changing the provider must start a new request generation")
	}

	m.handleAITestResult(AITestResultMsg{OK: true, Status: "3 models", Gen: stale,
		Models: []string{"old-model"}})

	if len(m.aiCfgModels) != 0 {
		t.Fatalf("a stale reply must not fill the list: %v", m.aiCfgModels)
	}
	if m.cfg.AI.Model == "old-model" {
		t.Fatal("a stale reply must not choose a model")
	}
}

// TestAISettingsEditAndCommit edits the Model field and commits it via Enter.
// TestAISettingsLoadsModelsOnOpen: the reason this feature exists is that
// choosing a model without knowing what the server has is guesswork, so opening
// the wizard asks the server.
func TestAISettingsLoadsModelsOnOpen(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	cmd := m.startAISettings()
	if cmd == nil {
		t.Fatal("opening the wizard must start a model probe")
	}
	if !m.aiCfgTest.running {
		t.Fatal("the probe should be running while the wizard is open")
	}

	m.handleAITestResult(AITestResultMsg{OK: true, Status: "3 models", Gen: m.aiCfgGen,
		Models: []string{"deepseek-r1", "llama3.2", "qwen3-coder"}})

	if len(m.aiCfgModels) != 3 {
		t.Fatalf("models = %v", m.aiCfgModels)
	}
	// An empty Model row means "whatever the server likes" — now we know.
	if m.cfg.AI.Model != "deepseek-r1" {
		t.Fatalf("model = %q, want the first model auto-filled", m.cfg.AI.Model)
	}
	if m.chatModel != m.cfg.AI.Model {
		t.Fatalf("the chat must follow the model the wizard chose: %q vs %q", m.chatModel, m.cfg.AI.Model)
	}
}

// TestAISettingsModelSelectPicksFromList: the model row offers what the server
// reported, and a pick applies immediately (Ctrl+S is only about persisting it).
func TestAISettingsModelSelectPicksFromList(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.startAISettings()
	m.handleAITestResult(AITestResultMsg{OK: true, Status: "3 models", Gen: m.aiCfgGen,
		Models: []string{"a-model", "b-model", "c-model"}})

	// The auto-filled value is the first model, and the list opens on it.
	if m.cfg.AI.Model != "a-model" {
		t.Fatalf("model = %q, want the auto-filled first entry", m.cfg.AI.Model)
	}
	pickAISelect(t, &m, 1, "c-model")
	if m.cfg.AI.Model != "c-model" {
		t.Fatalf("model = %q, want c-model", m.cfg.AI.Model)
	}
	if m.chatModel != "c-model" {
		t.Fatalf("the chat must follow the pick, got %q", m.chatModel)
	}
	// The cached provider is dropped so the next request uses the new model.
	if m.ai != nil || m.aiKey != "" {
		t.Fatal("picking a model must invalidate the cached provider")
	}
}

// TestAISettingsKeepsPinnedModel pins the rule that matters: the wizard may fill
// an empty field, but it must never overwrite a model the user chose — even one
// the server does not report (some servers filter the list).
func TestAISettingsKeepsPinnedModel(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.cfg.AI.Model = "my-private-fork"
	m.startAISettings()
	m.handleAITestResult(AITestResultMsg{OK: true, Status: "2 models", Gen: m.aiCfgGen,
		Models: []string{"a-model", "b-model"}})

	if m.cfg.AI.Model != "my-private-fork" {
		t.Fatalf("model = %q, want the user's choice untouched", m.cfg.AI.Model)
	}
	row := m.aiModelRow(">")
	if !containsStr(row, "my-private-fork") {
		t.Fatalf("the row must show the value: %q", row)
	}
	if !containsStr(row, m.t("ai.model_not_listed")) {
		t.Fatalf("the row must warn that the model is not in the list: %q", row)
	}
}

// TestAISettingsModelRowHints covers what the user actually sees: how many
// models were found, and what to do while the list is loading.
func TestAISettingsModelRowHints(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.cfg.AI.Model = "x"

	m.startAISettings()
	if row := m.aiModelRow(">"); !containsStr(row, "loading") {
		t.Fatalf("while loading, the row must say so: %q", row)
	}

	m.handleAITestResult(AITestResultMsg{OK: true, Status: "4 models", Gen: m.aiCfgGen,
		Models: []string{"a", "b", "c", "d"}})
	row := m.aiModelRow(">")
	if !containsStr(row, "4") || !containsStr(row, "Enter") {
		t.Fatalf("the row must show the count and the keys: %q", row)
	}

	// A failed probe drops the list: it belonged to a server we cannot reach.
	m.handleAITestResult(AITestResultMsg{OK: false, Status: "dial tcp: refused", Gen: m.aiCfgGen})
	if len(m.aiCfgModels) != 0 {
		t.Fatalf("a failed probe must clear the list, got %v", m.aiCfgModels)
	}
}

// TestAISettingsModelListSortedAndLoaded drives the real probe command against a
// local stub: the list must arrive sorted, because "the first model" is what the
// auto-pick uses and a shuffling list would make that arbitrary.
func TestAISettingsModelListSortedAndLoaded(t *testing.T) {
	isolateHomeConfig(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"z-last"},{"id":"a-first"},{"id":"m-middle"}]}`))
	}))
	defer srv.Close()

	m := New()
	m.cfg.AI.OllamaURL = srv.URL
	m.cfg.AI.Provider = "Groq" // an OpenAI-compatible provider, so /v1/models
	cmd := m.testAIConnection()
	if cmd == nil {
		t.Fatal("probe must start")
	}
	msg, ok := cmd().(AITestResultMsg)
	if !ok || !msg.OK {
		t.Fatalf("probe result = %+v", msg)
	}
	want := []string{"a-first", "m-middle", "z-last"}
	for i, w := range want {
		if msg.Models[i] != w {
			t.Fatalf("models = %v, want %v", msg.Models, want)
		}
	}
}

func TestAISettingsEditAndCommit(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.startAISettings()

	// Move to Model row.
	for i := 0; i < 1; i++ {
		m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if m.aiCfgField != 1 {
		t.Fatalf("field = %d, want 1", m.aiCfgField)
	}

	// Enter opens the list; typing a name by hand is the last entry in it.
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.aiCfgSelOpen {
		t.Fatal("Enter on the Model row must open the select")
	}
	for m.aiCfgSelItems[m.aiCfgSelIdx] != aiSelectTypeItem {
		m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.aiCfgEdit {
		t.Fatal("should be in edit mode after picking the type-a-name entry")
	}
	for _, r := range []rune("llama3.1") {
		m.handleAISettings(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.aiCfgEdit {
		t.Fatal("edit mode should close after commit")
	}
	if m.cfg.AI.Model != "llama3.1" {
		t.Fatalf("model = %q, want llama3.1", m.cfg.AI.Model)
	}
}

// TestAISettingsPaste verifies pasted text (bracketed-paste PasteMsg) lands in
// the field being edited instead of the editor buffer.
func TestAISettingsPaste(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.startAISettings()

	for i := 0; i < 1; i++ {
		m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.aiCfgSelOpen {
		t.Fatal("Enter on the Model row must open the select")
	}
	for m.aiCfgSelItems[m.aiCfgSelIdx] != aiSelectTypeItem {
		m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.aiCfgEdit {
		t.Fatal("should be in edit mode after picking the type-a-name entry")
	}

	next, _ := m.Update(tea.PasteMsg{Content: "deepseek-r1"})
	m = next.(Model)
	got := string(m.aiCfgIn)
	if !strings.HasSuffix(got, "deepseek-r1") {
		t.Fatalf("field = %q, want pasted text appended", got)
	}
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.HasSuffix(m.cfg.AI.Model, "deepseek-r1") {
		t.Fatalf("model = %q, want pasted text committed", m.cfg.AI.Model)
	}
}

// TestAISettingsEscCloses verifies Esc exits, and Esc inside edit reverts.
func TestAISettingsEscCloses(t *testing.T) {
	isolateHomeConfig(t)
	m := New()
	m.startAISettings()
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.aiCfgOpen {
		t.Fatal("wizard should close on Esc")
	}
}

// TestAISettingsSaveWritesConfig verifies Ctrl+S persists the [ai] section to
// the project config and reloads values.
func TestAISettingsSaveWritesConfig(t *testing.T) {
	isolateHomeConfig(t)
	dir := t.TempDir()
	m := New(dir)
	m.startAISettings()

	// Move to Context Max (field 4), edit, commit.
	for i := 0; i < 4; i++ {
		m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEnter})
	for i := 0; i < len("6000"); i++ {
		m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	for _, r := range []rune("9999") {
		m.handleAISettings(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m.handleAISettings(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.cfg.AI.ContextMax != 9999 {
		t.Fatalf("context_max = %d, want 9999 (before save)", m.cfg.AI.ContextMax)
	}

	m.handleAISettings(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})

	path := filepath.Join(dir, ".dmed.conf")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("config file not written: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "[ai]") || !strings.Contains(content, "context_max = 9999") {
		t.Fatalf("config file missing saved values:\n%s", content)
	}
	if !strings.Contains(m.msg, "AI settings saved") {
		t.Fatalf("msg = %q", m.msg)
	}
}
