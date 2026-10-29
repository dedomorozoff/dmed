package editor

import (
	"os"
	"testing"

	"dmed/internal/ai"
	"dmed/internal/lsp"
	"dmed/internal/vcs"
)

// TestToolBatchTouchesDisk pins the write-set rule that gates the two full
// project walks: RUN/EDIT/REPLACE (their legacy spellings included) can touch
// files; the rest of the surface - SEARCH, GLOB, LIST_DIR, the todo/ask/
// parking machinery - cannot, so a batch without those three walks nothing.
func TestToolBatchTouchesDisk(t *testing.T) {
	cases := map[string]bool{
		"SEARCH":      false,
		"LIST_DIR":    false,
		"GLOB":        false,
		"READ":        false,
		"TODO_WRITE":  false,
		"ASK_USER":    false,
		"SWITCH_MODE": false,
		"WEB_SEARCH":  false,
		"SUB_AGENT":   false,
		"RUN":         true,
		"EDIT":        true,
		"REPLACE":     true,
		// Legacy spellings the models reach for (any case).
		"write_file":  true,
		"edit_file":   true,
		"str_replace": true,
		"run_command": true,
	}
	for name, want := range cases {
		if got := toolBatchTouchesDisk([]ai.ToolCall{{ID: "t", Name: name}}); got != want {
			t.Errorf("toolBatchTouchesDisk(%q) = %v, want %v", name, got, want)
		}
	}
	if toolBatchTouchesDisk(nil) {
		t.Error("an empty batch never touches disk")
	}
}

// TestHandleLspFormatMsgAppliesEdits checks the LSP format result lands in the
// buffer of the open tab and keeps it dirty (the human keeps saving).
func TestHandleLspFormatMsgAppliesEdits(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "form.go", "bad layout\nfine\n")
	m := New(f)
	m.lspClient = &lsp.Client{} // stub: DidChange with a nil transport is a no-op

	// Replace the whole first line with the formatted one.
	msg := lspFormatMsg{edit: &lsp.WorkspaceEdit{Files: map[string][]lsp.TextEdit{
		f: {{StartLine: 0, StartCol: 0, EndLine: 0, EndCol: 10, NewText: "good layout"}},
	}}}
	m.handleLspFormatMsg(msg)

	if got := string(m.tabs[0].buf.LineAt(0)); got != "good layout" {
		t.Fatalf("formatted line = %q, want %q", got, "good layout")
	}
	if !m.tabs[0].buf.Dirty() {
		t.Fatal("a layout result must keep the buffer dirty (unsaved)")
	}
}

// TestHandleLspFormatMsgReportsClean checks the "nothing to lay out" reply
// reaches the status line instead of being swallowed silently.
func TestHandleLspFormatMsgReportsClean(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "a.go", "package a\n")
	m := New(f)
	m.lspClient = &lsp.Client{}
	m.handleLspFormatMsg(lspFormatMsg{})
	if m.msg == "" {
		t.Fatal("the already-formatted reply must surface a status message")
	}
}

// TestAgentUndoWithoutRepo pins the behaviour in a directory without git: the
// undo declines with a status line and spawns no work.
func TestAgentUndoWithoutRepo(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "a.txt", "x\n")
	m := New(f)
	m.repo = nil // pin the decline path: tests forge a repo-less editor
	if cmd := m.undoLastAgentApply(); cmd != nil {
		t.Fatal("undo without a repo must not spawn a command")
	}
	if m.msg == "" {
		t.Fatal("the decline must explain itself")
	}
}

// TestAgentUndoRevertsLastAgentCommit walks the whole undo path for a real
// .git directory: an "agent: ..." commit on top of history is reverted by
// undoLastAgentApply, the reverted file is reloaded into its open tab, and a
// "agent: revert <hash>" commit lands as the undo's own trace.
func TestAgentUndoRevertsLastAgentCommit(t *testing.T) {
	dir, f := initTestGitRepo(t)
	repo, err := vcs.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Agent commit: rewrite the tracked file, like acceptAgentReview does.
	if err := os.WriteFile(f, []byte("package main // agent touched\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := repo.Stage(f); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Commit("agent: touch the file"); err != nil {
		t.Fatal(err)
	}

	m := New(f)
	m.repo = repo

	cmd := m.undoLastAgentApply()
	if cmd == nil {
		t.Fatal("undo must be spawnable in a git repo")
	}
	msg, ok := cmd().(agentUndoMsg)
	if ok == false || msg.err != nil {
		t.Fatalf("undo must succeed, got err=%v", msg.err)
	}
	if len(msg.paths) != 1 || msg.paths[0] != "code.go" {
		t.Fatalf("reverted paths = %v", msg.paths)
	}

	m.handleAgentUndoMsg(msg)

	if data, err := os.ReadFile(f); err != nil || string(data) != "package main\n\nfunc main() {}\n" {
		t.Fatalf("file after undo = %q (err %v), want pre-agent content", data, err)
	}
	if got := string(m.tabs[0].buf.Text()); got != "package main\n\nfunc main() {}\n" {
		t.Fatalf("buffer after undo = %q, want the reloaded pre-agent content", got)
	}
}
