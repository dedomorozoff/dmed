package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dmed/internal/config"
)

func slashHarness(t *testing.T) Model {
	t.Helper()
	m := Model{root: t.TempDir()}
	m.cfg = config.Defaults()
	// Keep the home directory out of the resolution: tests must see the
	// project's templates plus the built-ins, not the developer's own.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	return m
}

func writeTemplate(t *testing.T, m Model, name, body string) {
	t.Helper()
	dir := filepath.Join(m.baseDir(), slashDirName, slashTemplateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSplitSlash(t *testing.T) {
	if n, a, ok := splitSlash("/refactor internal/agent please"); !ok || n != "refactor" || a != "internal/agent please" {
		t.Fatalf("got %q %q %v", n, a, ok)
	}
	if n, a, ok := splitSlash("/Test"); !ok || n != "test" || a != "" {
		t.Fatalf("case folding lost: %q %q", n, a)
	}
	if _, _, ok := splitSlash("just a question"); ok {
		t.Fatal("a plain message is not a command")
	}
	if n, _, ok := splitSlash("/"); !ok || n != "" {
		t.Fatalf(`bare "/" must ask for the listing, got %q %v`, n, ok)
	}
}

func TestExpandTemplateShapes(t *testing.T) {
	// Placeholder replaced.
	got := expandTemplate("# Title line\nRefactor {args} now.", "the rope")
	if strings.Contains(got, "# Title") || got != "Refactor the rope now." {
		t.Fatalf("placeholder body = %q", got)
	}
	// No placeholder: arguments go below, description line still dropped.
	got = expandTemplate("# Notes\nDo the thing", "extra")
	if got != "Do the thing\n\nextra" {
		t.Fatalf("appended body = %q", got)
	}
	// No arguments: the model is told to ask, not left guessing.
	got = expandTemplate("Do the thing {args}", "")
	if !strings.Contains(got, "ask the user") {
		t.Fatalf("empty args = %q", got)
	}
}

func TestBuiltinSlashCommandsExpand(t *testing.T) {
	m := slashHarness(t)
	prompt, notice, isCmd := m.expandSlash("/refactor internal/mcp")
	if !isCmd || notice != "" {
		t.Fatalf("built-in /refactor unknown: %q", notice)
	}
	if !strings.Contains(prompt, "internal/mcp") || !strings.Contains(prompt, "do not change what it does") {
		t.Fatalf("expansion = %q", prompt)
	}
	// The description line ("# ...") never reaches the model.
	if strings.Contains(prompt, "# ") {
		t.Fatalf("description leaked into the prompt: %q", prompt)
	}
}

func TestProjectTemplateOverridesBuiltin(t *testing.T) {
	m := slashHarness(t)
	writeTemplate(t, m, "refactor.md", "# my refactor\nRewrite {args} with my house style.")
	prompt, _, _ := m.expandSlash("/refactor x.go")
	if !strings.Contains(prompt, "house style") {
		t.Fatalf("the project template did not win: %q", prompt)
	}
}

func TestLuaTemplateRuns(t *testing.T) {
	m := slashHarness(t)
	writeTemplate(t, m, "changelog.lua", `
-- # returns a string directly
return "Summarize changes for " .. args .. " since v1"
`)
	prompt, notice, isCmd := m.expandSlash("/changelog HEAD")
	if !isCmd || notice != "" {
		t.Fatalf("lua command not resolved: %q", notice)
	}
	if prompt != "Summarize changes for HEAD since v1" {
		t.Fatalf("lua prompt = %q", prompt)
	}

	writeTemplate(t, m, "global.lua", `prompt = "look at " .. args`)
	prompt, _, _ = m.expandSlash("/global x")
	if prompt != "look at x" {
		t.Fatalf("global prompt = %q", prompt)
	}

	writeTemplate(t, m, "hangs.lua", `while true do end`)
	_, notice, _ = m.expandSlash("/hangs")
	if notice == "" || !strings.Contains(notice, "template /hangs failed") {
		t.Fatalf("a hanging template must hit its budget, not the editor: %q", notice)
	}

	writeTemplate(t, m, "silent.lua", `local x = 1`)
	_, notice, _ = m.expandSlash("/silent")
	if !strings.Contains(notice, "neither returned") {
		t.Fatalf("a silent template must report: %q", notice)
	}
}

func TestUnknownSlashAnswersWithTheListing(t *testing.T) {
	m := slashHarness(t)
	_, notice, isCmd := m.expandSlash("/nope hey")
	if !isCmd {
		t.Fatal("unknown name is still a command")
	}
	if !strings.Contains(notice, "unknown command /nope") || !strings.Contains(notice, "/refactor") {
		t.Fatalf("notice = %q", notice)
	}
	// Bare "/" and /help list without erroring.
	_, notice, _ = m.expandSlash("/")
	if !strings.HasPrefix(notice, "commands:") {
		t.Fatalf("bare slash = %q", notice)
	}
	_, notice, _ = m.expandSlash("/help")
	if !strings.Contains(notice, "/test") {
		t.Fatalf("help = %q", notice)
	}
}

func TestChatSubmitExpandsBeforeTheModel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	m := chatEditHarness(t, t.TempDir())
	m.chatModel = "test-model"
	m.chatIn = []rune("/test internal/rope")
	cmd := m.chatSubmit()
	if cmd == nil {
		t.Fatal("a known command must send the expanded prompt")
	}
	// The transcript holds the expansion, the prompt history the raw line.
	if len(m.chatMsgs) != 1 || !strings.Contains(m.chatMsgs[0].Content, "internal/rope") {
		t.Fatalf("sent message = %+v", m.chatMsgs)
	}
	if len(m.chatPrompts) == 0 || m.chatPrompts[len(m.chatPrompts)-1] != "/test internal/rope" {
		t.Fatalf("prompt history should keep the command: %v", m.chatPrompts)
	}

	// Unknown command: nothing goes out, the listing is shown, input cleared.
	m2 := chatEditHarness(t, t.TempDir())
	m2.chatModel = "test-model"
	m2.chatMsgs = nil
	m2.chatIn = []rune("/nope")
	if cmd := m2.chatSubmit(); cmd != nil {
		t.Fatal("an unknown command must not start a turn")
	}
	if m2.chatErr == "" || !strings.Contains(m2.chatErr, "unknown command") {
		t.Fatalf("notice = %q", m2.chatErr)
	}
	if len(m2.chatIn) != 0 {
		t.Fatal("the input was not cleared")
	}
	if len(m2.chatMsgs) != 0 {
		t.Fatalf("nothing must be appended to the conversation: %+v", m2.chatMsgs)
	}
}

func TestTemplateDirsPrecedence(t *testing.T) {
	m := slashHarness(t)
	dirs := m.slashTemplateDirs()
	if len(dirs) != 2 || !strings.Contains(dirs[0], m.baseDir()) {
		t.Fatalf("dirs = %v, want project first then home", dirs)
	}
	// A file in the project beats one of the same name in home:
	writeTemplate(t, m, "own.md", "# project\nfrom the project")
	homeDir := dirs[1] // already <home>/.dmed/templates
	if err := os.MkdirAll(homeDir, 0o755); err == nil {
		_ = os.WriteFile(filepath.Join(homeDir, "own.md"), []byte("# home\nfrom home"), 0o644)
	}
	prompt, _, _ := m.expandSlash("/own")
	if !strings.HasPrefix(prompt, "from the project") || strings.Contains(prompt, "from home") {
		t.Fatalf("precedence lost: %q", prompt)
	}
}
