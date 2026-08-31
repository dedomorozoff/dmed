package vcs

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func initTestRepo(t *testing.T) (string, *Repo) {
	t.Helper()
	dir := t.TempDir()

	r, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}

	f := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(f, []byte("line1\nline2\nline3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w, err := r.Worktree()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := w.Add("sample.txt"); err != nil {
		t.Fatal(err)
	}

	_, err = w.Commit("initial commit", &git.CommitOptions{
		Author: &object.Signature{Name: "tester", Email: "test@test.com", When: time.Now()},
	})
	if err != nil {
		t.Fatal(err)
	}

	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, repo
}

func TestGitBranchAndStatus(t *testing.T) {
	dir, repo := initTestRepo(t)
	if repo.Branch() == "" {
		t.Fatal("expected non-empty branch name")
	}

	// Clean initially
	if repo.StatusSummary() != "clean" {
		t.Fatalf("expected clean status, got %s", repo.StatusSummary())
	}

	// Modify file
	f := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(f, []byte("line1\nline2 modified\nline3\nline4\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if repo.StatusSummary() != "~1" {
		t.Fatalf("expected ~1 status, got %s", repo.StatusSummary())
	}
}

func TestGitInitCreatesDefaultBranch(t *testing.T) {
	dir := t.TempDir()
	repo, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	// main exists right after init, backed by an empty initial commit.
	if got := repo.Branch(); got != "main" {
		t.Fatalf("expected branch main right after init, got %q", got)
	}
	entries, err := repo.Log(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected the initial commit in history, got %d entries", len(entries))
	}
	branches, err := repo.Branches()
	if err != nil {
		t.Fatal(err)
	}
	if len(branches) != 1 || branches[0] != "main" {
		t.Fatalf("expected the current branch main right after init, got %v", branches)
	}

	// Untracked files should be visible right after init.
	f := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(f, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := repo.StatusFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "sample.txt" || files[0].Worktree != StatusUntracked {
		t.Fatalf("expected one untracked sample.txt right after init, got %+v", files)
	}
	if err := repo.Stage(f); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Commit("add sample"); err != nil {
		t.Fatalf("commit on main failed: %v", err)
	}
	if got := repo.Branch(); got != "main" {
		t.Fatalf("expected main after commit, got %q", got)
	}
	if got := repo.StatusSummary(); got != "clean" {
		t.Fatalf("expected clean status, got %s", got)
	}
}

func TestGitCreateBranchAndSwitchBack(t *testing.T) {
	dir := t.TempDir()
	repo, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Creating a branch keeps main around, and switching back to it works.
	if err := repo.CreateBranch("experiment"); err != nil {
		t.Fatalf("create branch failed: %v", err)
	}
	if got := repo.Branch(); got != "experiment" {
		t.Fatalf("expected experiment, got %q", got)
	}
	branches, err := repo.Branches()
	if err != nil {
		t.Fatal(err)
	}
	if len(branches) != 2 || !slices.Contains(branches, "main") || !slices.Contains(branches, "experiment") {
		t.Fatalf("expected both branches listed, got %v", branches)
	}
	if err := repo.SwitchBranch("main"); err != nil {
		t.Fatalf("switch back to main failed: %v", err)
	}
	if got := repo.Branch(); got != "main" {
		t.Fatalf("expected main after switch back, got %q", got)
	}
}

func TestGitCreateBranchOnUnbornRepoKeepsCurrentBranch(t *testing.T) {
	// A repo created externally (e.g. plain `git init`) has no commits yet:
	// HEAD is unborn. Creating a branch must not lose the current branch, and
	// both branches must be switchable afterwards.
	dir := t.TempDir()
	if _, err := git.PlainInit(dir, false); err != nil {
		t.Fatal(err)
	}
	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	orig := repo.Branch()
	if orig == "" {
		t.Fatal("expected unborn HEAD to resolve to a branch name (e.g. master)")
	}

	if err := repo.CreateBranch("feature"); err != nil {
		t.Fatalf("create branch on unborn repo failed: %v", err)
	}
	if got := repo.Branch(); got != "feature" {
		t.Fatalf("expected feature to be current, got %q", got)
	}
	branches, err := repo.Branches()
	if err != nil {
		t.Fatal(err)
	}
	if len(branches) != 2 || !slices.Contains(branches, orig) || !slices.Contains(branches, "feature") {
		t.Fatalf("expected both branches to exist, got %v", branches)
	}
	entries, err := repo.Log(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 commit on feature (the initial one), got %d", len(entries))
	}

	if err := repo.SwitchBranch(orig); err != nil {
		t.Fatalf("switch back to %q failed: %v", orig, err)
	}
	if got := repo.Branch(); got != orig {
		t.Fatalf("expected %q after switch back, got %q", orig, got)
	}
	entries, err = repo.Log(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 commit on %q, got %d", orig, len(entries))
	}
	if err := repo.SwitchBranch("feature"); err != nil {
		t.Fatalf("switch to feature failed: %v", err)
	}

	f := filepath.Join(dir, "work.txt")
	if err := os.WriteFile(f, []byte("line1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := repo.StatusFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "work.txt" || files[0].Worktree != StatusUntracked {
		t.Fatalf("expected untracked work.txt on feature, got %+v", files)
	}
}

func TestGitDiffBuffer(t *testing.T) {
	dir, repo := initTestRepo(t)
	f := filepath.Join(dir, "sample.txt")

	// Diff clean buffer
	cleanDiff := repo.DiffBuffer(f, "line1\nline2\nline3\n")
	if len(cleanDiff.Hunks) != 0 {
		t.Fatalf("expected 0 hunks for clean buffer, got %d", len(cleanDiff.Hunks))
	}

	// Diff with added line
	modDiff := repo.DiffBuffer(f, "line1\nline2\nline3\nline4 added\n")
	if len(modDiff.Hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(modDiff.Hunks))
	}
	if modDiff.Hunks[0].Type != DiffAdded {
		t.Fatalf("expected DiffAdded hunk, got %v", modDiff.Hunks[0].Type)
	}
}
