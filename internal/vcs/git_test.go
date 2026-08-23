package vcs

import (
	"os"
	"path/filepath"
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
