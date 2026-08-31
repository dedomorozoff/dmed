package agent

import (
	"errors"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
)

type fakeGit struct {
	staged    []string
	commits   []string
	hash      plumbing.Hash
	failStage string
}

func (f *fakeGit) Stage(path string) error {
	if path == f.failStage {
		return errors.New("stage failed")
	}
	f.staged = append(f.staged, path)
	return nil
}

func (f *fakeGit) Commit(msg string) (plumbing.Hash, error) {
	f.commits = append(f.commits, msg)
	return f.hash, nil
}

func TestCommitStagesAndCommitsOnce(t *testing.T) {
	g := &fakeGit{hash: plumbing.NewHash("abc")}
	bus := &fakeBus{}
	c := &Committer{Repo: g, Bus: bus}

	err := c.Commit([]string{"a.go", "b.go"}, "refactor the parser")
	if err != nil {
		t.Fatalf("commit failed: %v", err)
	}
	if len(g.staged) != 2 {
		t.Fatalf("expected 2 staged paths, got %v", g.staged)
	}
	if len(g.commits) != 1 {
		t.Fatalf("expected exactly 1 commit, got %d", len(g.commits))
	}
	if g.commits[0] != "agent: refactor the parser" {
		t.Fatalf("message = %q", g.commits[0])
	}

	// Notification events for each path.
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.events) != 4 {
		t.Fatalf("expected 4 events (file+git per path), got %d", len(bus.events))
	}
}

func TestCommitSkipsGitWhenNilRepo(t *testing.T) {
	bus := &fakeBus{}
	c := &Committer{Bus: bus}

	if err := c.Commit([]string{"a.go"}, "task"); err != nil {
		t.Fatalf("commit failed: %v", err)
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.events) != 2 {
		t.Fatalf("expected 2 events when git skipped, got %d", len(bus.events))
	}
}

func TestCommitStageFailure(t *testing.T) {
	g := &fakeGit{failStage: "b.go"}
	bus := &fakeBus{}
	c := &Committer{Repo: g, Bus: bus}

	if err := c.Commit([]string{"a.go", "b.go"}, "task"); err == nil {
		t.Fatalf("expected stage failure")
	}
	if len(g.staged) != 1 {
		t.Fatalf("expected only a.go staged, got %v", g.staged)
	}
}

func TestFirstLineTruncates(t *testing.T) {
	long := "this is a very long task description that should be truncated down to a reasonable commit subject line length"
	got := firstLine(long)
	if len(got) > 63 {
		t.Fatalf("subject too long: %q (%d)", got, len(got))
	}
	if got != firstLine(long) {
		t.Fatalf("unstable truncation")
	}
}
