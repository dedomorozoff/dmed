package editor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dmed/internal/ai"
)

func TestChatToolDefsIncludesCoreTools(t *testing.T) {
	defs := chatToolDefs()
	names := map[string]bool{}
	for _, d := range defs {
		names[d.Name] = true
	}
	for _, want := range []string{"READ", "SEARCH", "RUN", "REPLACE", "EDIT"} {
		if !names[want] {
			t.Fatalf("missing tool %s in %v", want, names)
		}
	}
}

func TestExecChatToolReadAndSearch(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "hello.go"), "package main\n// greeting\n")
	writeTestFile(t, filepath.Join(dir, "other.txt"), "nothing here")

	m := Model{root: dir}

	res, chg := m.execChatTool(ai.ToolCall{Name: "READ", Args: `{"arg":"hello.go"}`})
	if chg != nil {
		t.Fatalf("READ must not produce a change")
	}
	if !containsStr(res, "greeting") {
		t.Fatalf("READ result missing content: %q", res)
	}
	if containsStr(res, "[READ error]") {
		t.Fatalf("READ errored: %q", res)
	}

	res, chg = m.execChatTool(ai.ToolCall{Name: "SEARCH", Args: `{"arg":"greeting"}`})
	if chg != nil {
		t.Fatalf("SEARCH must not produce a change")
	}
	if !containsStr(res, "hello.go") {
		t.Fatalf("SEARCH result missing file: %q", res)
	}

	res, _ = m.execChatTool(ai.ToolCall{Name: "READ", Args: `{"arg":"missing.go"}`})
	if !containsStr(res, "[READ error]") {
		t.Fatalf("expected error for missing file: %q", res)
	}
}

func TestExecChatToolEditProposesChangeButDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "t.txt"), "old content\n")
	m := Model{root: dir}

	res, chg := m.execChatTool(ai.ToolCall{Name: "EDIT", Args: `{"path":"t.txt","content":"new content"}`})
	if chg == nil {
		t.Fatalf("EDIT must propose a change, got %q", res)
	}
	if chg.Path != filepath.Join(dir, "t.txt") {
		t.Fatalf("change path = %q", chg.Path)
	}
	if chg.Orig != "old content\n" || chg.New != "new content\n" {
		t.Fatalf("change = orig %q new %q", chg.Orig, chg.New)
	}
	// The file must be untouched until the diff review accepts it.
	data, err := os.ReadFile(filepath.Join(dir, "t.txt"))
	if err != nil || string(data) != "old content\n" {
		t.Fatalf("file must not change before review: %q err=%v", string(data), err)
	}
	if !containsStr(res, "proposed") {
		t.Fatalf("result should mention a proposal: %q", res)
	}
}

func TestExecChatToolEditCreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	m := Model{root: dir}

	_, chg := m.execChatTool(ai.ToolCall{Name: "EDIT", Args: `{"path":"new/file.txt","content":"hi"}`})
	if chg == nil {
		t.Fatalf("EDIT of a missing file must propose a create")
	}
	if chg.Orig != "" || chg.New != "hi\n" {
		t.Fatalf("create change = orig %q new %q", chg.Orig, chg.New)
	}
	if _, err := os.Stat(filepath.Join(dir, "new", "file.txt")); !os.IsNotExist(err) {
		t.Fatalf("create must not land before review")
	}
}

func TestExecChatToolEditNoChangeIsSkipped(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "t.txt"), "same\n")
	m := Model{root: dir}

	res, chg := m.execChatTool(ai.ToolCall{Name: "EDIT", Args: `{"path":"t.txt","content":"same"}`})
	if chg != nil {
		t.Fatalf("no-change edit must not propose, got change")
	}
	if !containsStr(res, "no change") {
		t.Fatalf("result = %q", res)
	}
}

func TestChatSearchReturnsLineLocations(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.go"), "line one\nline two\nthree\n")
	m := Model{root: dir}
	args, _ := json.Marshal(map[string]string{"arg": "two"})
	res, chg := m.execChatTool(ai.ToolCall{Name: "SEARCH", Args: string(args)})
	if chg != nil {
		t.Fatalf("SEARCH must not produce a change")
	}
	if !containsStr(res, "a.go:2") {
		t.Fatalf("SEARCH should locate line 2: %q", res)
	}
}

func TestExecChatToolReplaceProposesChange(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "t.txt"), "alpha beta\nomega\n")
	m := Model{root: dir}
	args, _ := json.Marshal(map[string]string{"path": "t.txt", "search": "beta", "replace": "GAMMA"})
	res, chg := m.execChatTool(ai.ToolCall{Name: "REPLACE", Args: string(args)})
	if chg == nil {
		t.Fatalf("REPLACE must propose a change: %q", res)
	}
	if chg.Orig != "alpha beta\nomega\n" {
		t.Fatalf("orig = %q", chg.Orig)
	}
	if !containsStr(chg.New, "alpha GAMMA") {
		t.Fatalf("new = %q", chg.New)
	}
	data, err := os.ReadFile(filepath.Join(dir, "t.txt"))
	if err != nil || string(data) != "alpha beta\nomega\n" {
		t.Fatalf("file must not change before review: %q err=%v", string(data), err)
	}
}

func TestExecChatToolReplaceMissingBlock(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "t.txt"), "one two\n")
	m := Model{root: dir}
	args, _ := json.Marshal(map[string]string{"path": "t.txt", "search": "zzz", "replace": "x"})
	res, chg := m.execChatTool(ai.ToolCall{Name: "REPLACE", Args: string(args)})
	if chg != nil || !containsStr(res, "not found") {
		t.Fatalf("want not-found error, got %q chg=%v", res, chg != nil)
	}
}

func TestRunBlockedWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	m := Model{root: dir}
	m.cfg.AI.AllowRun = "never"
	args, _ := json.Marshal(map[string]string{"arg": "echo hi"})
	res, chg := m.execChatTool(ai.ToolCall{Name: "RUN", Args: string(args)})
	if chg != nil || !containsStr(res, "blocked") {
		t.Fatalf("want blocked message, got %q", res)
	}
}

func TestToolArgSummary(t *testing.T) {
	if s := toolArgSummary(ai.ToolCall{Name: "EDIT", Args: `{"path":"a/b.go","content":"x"}`}); s != "a/b.go" {
		t.Fatalf("edit summary = %q", s)
	}
	if s := toolArgSummary(ai.ToolCall{Name: "RUN", Args: `{"arg":"go test ./..."}`}); s != "go test ./..." {
		t.Fatalf("run summary = %q", s)
	}
}

func TestCompactLinesTruncatesLongOutput(t *testing.T) {
	in := "l0\nl1\nl2\nl3\nl4\nl5\nl6\nl7"
	got := compactLines(in, 80, 4)
	if len(got) != 5 {
		t.Fatalf("want 4 lines + overflow marker, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[4], "more lines") {
		t.Fatalf("want overflow marker, got %q", got[4])
	}
}

func TestCompactLinesCapsWidth(t *testing.T) {
	got := compactLines("abcdefghij", 5, 10)
	if strings.ContainsAny(got[0], "fghij") {
		t.Fatalf("line not capped to width: %q", got[0])
	}
}

func TestChatSearchRegexMode(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.go"), "line one\nline two\nthree\n")

	m := Model{root: dir}

	// Regex matches lines containing "line" followed by a space.
	args, _ := json.Marshal(map[string]any{"arg": `line\s+\w+`, "regex": true})
	res, chg := m.execChatTool(ai.ToolCall{Name: "SEARCH", Args: string(args)})
	if chg != nil {
		t.Fatalf("SEARCH must not produce a change")
	}
	if !containsStr(res, "a.go:1") || !containsStr(res, "a.go:2") {
		t.Fatalf("regex should match lines 1 and 2: %q", res)
	}
	if containsStr(res, "a.go:3") {
		t.Fatalf("regex should not match line 3: %q", res)
	}

	// Invalid regex returns an error, not a panic.
	args, _ = json.Marshal(map[string]any{"arg": `[invalid(`, "regex": true})
	res, _ = m.execChatTool(ai.ToolCall{Name: "SEARCH", Args: string(args)})
	if !containsStr(res, "invalid regex") {
		t.Fatalf("invalid regex should error gracefully: %q", res)
	}
}

func TestChatSearchLiteralStillWorks(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.go"), "line one\nline two\nthree\n")

	m := Model{root: dir}
	// Plain literal search (regex:false) behaves as before.
	args, _ := json.Marshal(map[string]any{"arg": "two", "regex": false})
	res, _ := m.execChatTool(ai.ToolCall{Name: "SEARCH", Args: string(args)})
	if !containsStr(res, "a.go:2") {
		t.Fatalf("literal search should locate line 2: %q", res)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
