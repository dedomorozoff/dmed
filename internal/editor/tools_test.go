package editor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseToolCallsEmpty(t *testing.T) {
	if tools := parseToolCalls("just a plain answer, no tools"); len(tools) != 0 {
		t.Fatalf("want no tools, got %+v", tools)
	}
}

func TestParseToolCallsRead(t *testing.T) {
	reply := "Let me look.\n=== TOOL: READ: main.go ===\nSome prose after."
	tools := parseToolCalls(reply)
	if len(tools) != 1 {
		t.Fatalf("want 1 tool, got %d", len(tools))
	}
	if tools[0].name != "READ" || tools[0].arg != "main.go" {
		t.Fatalf("got %+v", tools[0])
	}
}

func TestParseToolCallsEditBlock(t *testing.T) {
	reply := "I'll change the file.\n" +
		"=== TOOL: EDIT: file.go ===\n" +
		"package x\n\nfunc main() {}\n" +
		"=== END TOOL ===\n" +
		"Done."
	tools := parseToolCalls(reply)
	if len(tools) != 1 {
		t.Fatalf("want 1 tool, got %d (%+v)", len(tools), tools)
	}
	tc := tools[0]
	if tc.name != "EDIT" || tc.arg != "file.go" {
		t.Fatalf("got name=%q arg=%q", tc.name, tc.arg)
	}
	if tc.body != "package x\n\nfunc main() {}" {
		t.Fatalf("body = %q", tc.body)
	}
}

func TestParseToolCallsMultipleAndEditEndsAtNextMarker(t *testing.T) {
	reply := "=== TOOL: EDIT: a.go ===\n" +
		"content A\n" +
		"=== TOOL: READ: b.go ===\n" +
		"after"
	tools := parseToolCalls(reply)
	if len(tools) != 2 {
		t.Fatalf("want 2 tools, got %d (%+v)", len(tools), tools)
	}
	if tools[0].name != "EDIT" || tools[0].body != "content A" {
		t.Fatalf("edit = %+v", tools[0])
	}
	if tools[1].name != "READ" || tools[1].arg != "b.go" {
		t.Fatalf("read = %+v", tools[1])
	}
}

func TestExecuteToolReadAndSearch(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "hello.go"), "package main\n// greeting\n")
	writeTestFile(t, filepath.Join(dir, "other.txt"), "nothing here")

	m := Model{root: dir}

	res := executeTool(&m, toolCall{name: "READ", arg: "hello.go"})
	if !containsStr(res, "greeting") {
		t.Fatalf("READ result missing content: %q", res)
	}
	if containsStr(res, "[READ error]") {
		t.Fatalf("READ errored: %q", res)
	}

	res = executeTool(&m, toolCall{name: "SEARCH", arg: "greeting"})
	if !containsStr(res, "hello.go") {
		t.Fatalf("SEARCH result missing file: %q", res)
	}

	res = executeTool(&m, toolCall{name: "READ", arg: "missing.go"})
	if !containsStr(res, "[READ error]") {
		t.Fatalf("expected error for missing file: %q", res)
	}
}

func TestExecuteToolEdit(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "t.txt"), "old content\n")
	m := Model{root: dir}

	res := executeTool(&m, toolCall{name: "EDIT", arg: "t.txt", body: "new content"})
	if !containsStr(res, "[EDIT applied]") {
		t.Fatalf("EDIT result = %q", res)
	}
	data, err := os.ReadFile(filepath.Join(dir, "t.txt"))
	if err != nil || string(data) != "new content" {
		t.Fatalf("file after edit = %q err=%v", string(data), err)
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
