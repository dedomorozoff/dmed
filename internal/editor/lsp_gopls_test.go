package editor

import (
	"os/exec"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// TestGoplsTypedCompletionIntegration drives the real editor code path end to
// end: open a tiny Go module, place the cursor right after "fmt.", type a
// letter and verify gopls' type-aware members (Print/Printf/Println) land in
// the popup. Skipped when gopls is not installed so CI stays green.
func TestGoplsTypedCompletionIntegration(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skipf("no gopls on PATH")
	}
	dir := t.TempDir()
	writeTemp(t, dir, "go.mod", "module demo\n\ngo 1.26\n")
	f := writeTemp(t, dir, "main.go", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.\n}\n")

	m := New(dir, f)
	m.width, m.height = 100, 24

	// Cursor right after the dot on the last code line.
	m.cur().buf.SetCursor(5, 5)

	m.ensureLSP()
	if m.lspClient == nil {
		t.Fatal("LSP client did not start")
	}
	defer m.lspClient.Close()

	// Type one letter to form the prefix "P"; the popup must open even though
	// the source contains no buffer words starting with an uppercase P.
	m = press(m, tea.KeyPressMsg{Text: "P"})
	if !m.complOpen {
		t.Fatal("popup did not open after typing P")
	}

	// press() drops tea.Cmds; run the same async request the app would run and
	// feed its result back into the model.
	cmd := m.lspCompletionCmd()
	if cmd == nil {
		t.Fatal("no lsp completion cmd")
	}
	ch := make(chan lspCompletionMsg, 1)
	start := time.Now()
	go func() {
		if cm, ok := cmd().(lspCompletionMsg); ok {
			ch <- cm
		}
	}()
	var res lspCompletionMsg
	select {
	case res = <-ch:
	case <-time.After(75 * time.Second):
		t.Fatal("gopls completion did not answer within 75s")
	}
	if res.err != nil {
		t.Fatalf("lsp error: %v", res.err)
	}
	t.Logf("gopls answered in %v with %d items", time.Since(start), len(res.items))

	next, _ := m.Update(res)
	m = next.(Model)
	found := false
	for _, c := range m.complItems {
		if c == "Print" || c == "Printf" || c == "Println" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no fmt members in merged candidates: %v", m.complItems)
	}
}