package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dmed/internal/ai"
)

// TestFilterToolDefsWhitelist pins the contract small local models depend on:
// tools_enabled shrinks the tool list, tools_disabled trims it further, and
// legacy spellings resolve to the shipped names.
func TestFilterToolDefsWhitelist(t *testing.T) {
	all := chatToolDefs()

	got := toolDefNames(filterToolDefs(all, []string{"read", "search"}, nil))
	if want := []string{"READ", "SEARCH"}; !equalStrings(got, want) {
		t.Fatalf("whitelist = %v, want %v", got, want)
	}

	// The whitelist wins on names the model is likely to reach for.
	got = toolDefNames(filterToolDefs(all, []string{"read_file", "write_file", "run"}, nil))
	if want := []string{"READ", "RUN", "EDIT"}; !equalStrings(got, want) {
		t.Fatalf("legacy whitelist = %v, want %v (registry order)", got, want)
	}

	got = toolDefNames(filterToolDefs(all, nil, []string{"RUN", "edit"}))
	for _, n := range got {
		if n == "RUN" || n == "EDIT" {
			t.Fatalf("blacklist did not remove %s: %v", n, got)
		}
	}

	// Blacklist is applied after the whitelist, so a curated set can be
	// trimmed further without editing it.
	got = toolDefNames(filterToolDefs(all, []string{"READ", "SEARCH", "RUN"}, []string{"RUN"}))
	if want := []string{"READ", "SEARCH"}; !equalStrings(got, want) {
		t.Fatalf("whitelist + blacklist = %v, want %v", got, want)
	}

	if len(filterToolDefs(all, nil, nil)) != len(all) {
		t.Fatal("no configuration must leave every tool available")
	}
}

// TestActiveChatToolDefsUsesConfig checks the wiring: the per-turn tool list is
// the registry filtered by the model's configuration, so switching models or
// editing .dmed.conf takes effect on the next turn.
func TestActiveChatToolDefsUsesConfig(t *testing.T) {
	m := New()
	m.cfg.AI.ToolsEnabled = []string{"READ"}
	m.cfg.AI.ToolsDisabled = []string{"RUN"}

	got := toolDefNames(m.activeChatToolDefs())
	if want := []string{"READ"}; !equalStrings(got, want) {
		t.Fatalf("active tools = %v, want %v", got, want)
	}
}

func TestLegacyToolNamesResolve(t *testing.T) {
	cases := map[string]string{
		"read_file":   "READ",
		"write_file":  "EDIT",
		"edit_file":   "REPLACE",
		"grep":        "SEARCH",
		"run_command": "RUN",
		"glob":        "GLOB",
		"list_dir":    "LIST_DIR",
		"READ":        "READ", // already shipped
		"NONSENSE":    "NONSENSE",
	}
	for in, want := range cases {
		if got := normalizeToolName(in); got != want {
			t.Errorf("normalizeToolName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestExecChatToolAcceptsLegacyNames verifies the alias table is wired into
// dispatch, not just into the filter: a model that says read_file must actually
// read the file.
func TestExecChatToolAcceptsLegacyNames(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.go"), "package main\n")
	m := Model{root: dir}

	res, _ := toolText(t, &m, "read_file", `{"arg":"a.go"}`)
	if containsStr(res, "[READ error]") || !containsStr(res, "package main") {
		t.Fatalf("read_file must read: %q", res)
	}
	res, _ = toolText(t, &m, "grep", `{"arg":"package"}`)
	if !containsStr(res, "a.go:1") {
		t.Fatalf("grep must search: %q", res)
	}
}

// TestChatListDir pins the output shape: directories first, kind and size for
// files, no confirmation needed, and skipped trees omitted.
func TestChatListDir(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "main.go"), "package main\n")
	writeTestFile(t, filepath.Join(dir, "sub", "x.go"), "x\n")
	if err := os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dir, "node_modules", "pkg", "index.js"), "//\n")

	m := Model{root: dir}
	res, chg := toolText(t, &m, "LIST_DIR", `{"arg":"."}`)
	if chg != nil {
		t.Fatal("LIST_DIR must not propose a change")
	}
	if containsStr(res, "[LIST_DIR error]") {
		t.Fatalf("LIST_DIR errored: %q", res)
	}
	if !containsStr(res, "sub/") {
		t.Fatalf("want the sub directory listed: %q", res)
	}
	if containsStr(res, "node_modules") {
		t.Fatalf("skipped trees must stay out of the listing: %q", res)
	}
	if !containsStr(res, "main.go\t") {
		t.Fatalf("want a name and size for files: %q", res)
	}
	// Directories come before files so the model reads a shape first.
	if strings.Index(res, "sub/") > strings.Index(res, "main.go") {
		t.Fatalf("directories must be listed first: %q", res)
	}

	// An empty arg means the project root, and a missing dir is an error the
	// model can learn from.
	res, _ = toolText(t, &m, "LIST_DIR", `{}`)
	if !containsStr(res, "main.go") {
		t.Fatalf("empty arg must list the root: %q", res)
	}
	res, _ = toolText(t, &m, "LIST_DIR", `{"arg":"nope"}`)
	if !containsStr(res, "[LIST_DIR error]") {
		t.Fatalf("missing dir must error: %q", res)
	}
}

func TestChatListDirRespectsRestrictToRoot(t *testing.T) {
	m := Model{root: t.TempDir()}
	m.cfg.AI.RestrictToRoot = true
	res, _ := toolText(t, &m, "LIST_DIR", `{"arg":"../.."}`)
	if !containsStr(res, "outside project root") {
		t.Fatalf("LIST_DIR must honour restrict_to_root: %q", res)
	}
}

func TestChatGlob(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "main.go"), "package main\n")
	writeTestFile(t, filepath.Join(dir, "internal", "agent", "queue.go"), "package agent\n")
	writeTestFile(t, filepath.Join(dir, "internal", "agent", "queue_test.go"), "package agent\n")
	writeTestFile(t, filepath.Join(dir, "README.md"), "# dmed\n")

	m := Model{root: dir}

	res, chg := toolText(t, &m, "GLOB", `{"arg":"**/*_test.go"}`)
	if chg != nil {
		t.Fatal("GLOB must not propose a change")
	}
	if !containsStr(res, "internal/agent/queue_test.go") {
		t.Fatalf("** must match at any depth: %q", res)
	}
	if containsStr(res, "queue.go") && !containsStr(res, "queue_test.go") {
		t.Fatalf("GLOB matched too much: %q", res)
	}

	res, _ = toolText(t, &m, "GLOB", `{"arg":"internal/*/queue.go"}`)
	if !containsStr(res, "internal/agent/queue.go") {
		t.Fatalf("multi-segment pattern failed: %q", res)
	}

	res, _ = toolText(t, &m, "GLOB", `{"arg":"*.md"}`)
	if !containsStr(res, "README.md") || containsStr(res, "main.go") {
		t.Fatalf("root-only pattern must not recurse: %q", res)
	}

	res, _ = toolText(t, &m, "GLOB", `{"arg":"internal"}`)
	if !containsStr(res, "internal") {
		t.Fatalf("a directory pattern must match the directory: %q", res)
	}

	res, _ = toolText(t, &m, "GLOB", `{"arg":"*.rs"}`)
	if !containsStr(res, "no matches") {
		t.Fatalf("want a clear no-match result: %q", res)
	}

	res, _ = toolText(t, &m, "GLOB", `{"arg":"["}`)
	if !containsStr(res, "invalid pattern") {
		t.Fatalf("a broken pattern must error, not panic: %q", res)
	}
	res, _ = toolText(t, &m, "GLOB", `{"arg":"/abs/path"}`)
	if !containsStr(res, "must be relative") {
		t.Fatalf("absolute patterns must be rejected: %q", res)
	}
}

// TestChatGlobSkipsIgnoredTrees pins that the walk does not descend into the
// places every other tool skips either: a *.go pattern must stay out of a
// vendored tree, or it becomes needlessly expensive on a big repository.
func TestChatGlobSkipsIgnoredTrees(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.go"), "package a\n")
	writeTestFile(t, filepath.Join(dir, "node_modules", "dep", "b.go"), "package b\n")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dir, ".git", "hook.go"), "package hook\n")

	m := Model{root: dir}
	res, _ := toolText(t, &m, "GLOB", `{"arg":"**/*.go"}`)
	if containsStr(res, "node_modules") || containsStr(res, "hook.go") {
		t.Fatalf("GLOB must not descend into ignored trees: %q", res)
	}
	if !containsStr(res, "a.go") {
		t.Fatalf("want the real match: %q", res)
	}
}

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"**/*.go", "main.go", true},
		{"**/*.go", "internal/agent/queue.go", true},
		{"**/*.go", "main.py", false},
		{"internal/*/queue.go", "internal/agent/queue.go", true},
		{"internal/*/queue.go", "internal/agent/sub/queue.go", false},
		{"*.go", "main.go", true},
		{"*.go", "sub/main.go", false},
		{"**", "a/b/c.txt", true},
		{"internal/**", "internal/a/b.go", true},
		{"README.md", "README.md", true},
		{"README.md", "docs/README.md", false},
		{"docs/", "docs", false}, // a trailing slash is not special here
	}
	for _, c := range cases {
		if got := matchGlob(c.pattern, c.name); got != c.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

// TestGlobMayContainNeverOverPrunes: a wrong prune means a silently missing
// file, which is far worse than a slower walk, so the conservative cases must
// hold even where the pattern reaches deeper than the path.
func TestGlobMayContainNeverOverPrunes(t *testing.T) {
	always := []struct{ pattern, dir string }{
		{"a/b/*.go", "a"},
		{"a/b/*.go", "a/b"},
		{"**/*.go", "internal"},
		{"**/*.go", "internal/agent"},
		{"a/**", "a"},
		{"a/**", "a/b"},
		{"**", "anything"},
	}
	for _, c := range always {
		if !globMayContain(c.pattern, c.dir) {
			t.Errorf("globMayContain(%q, %q) = false, must keep walking", c.pattern, c.dir)
		}
	}
	pruned := []struct{ pattern, dir string }{
		{"*.go", "internal"},
		{"internal/*.go", "other"},
		{"internal/*/queue.go", "cmd"},
	}
	for _, c := range pruned {
		if globMayContain(c.pattern, c.dir) {
			t.Errorf("globMayContain(%q, %q) = true, may prune", c.pattern, c.dir)
		}
	}
}

func TestWalkProjectSkipsConfiguredDirs(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.txt"), "a\n")
	writeTestFile(t, filepath.Join(dir, "skipme", "b.txt"), "b\n")

	m := Model{root: dir}
	m.cfg.Editor.SkippedDirs = []string{"skipme"}

	var seen []string
	if err := walkProject(dir, m.newProjectSkip(), func(p string, _ os.FileInfo) error {
		seen = append(seen, filepath.Base(p))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != "a.txt" {
		t.Fatalf("walked %v, want only a.txt", seen)
	}
}

func toolDefNames(defs []ai.ToolDef) []string {
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		out = append(out, d.Name)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
