package vcs

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
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

// TestGitDiffBuffer verifies line-by-line diff against HEAD.
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

func writeAndCommit(t *testing.T, repo *Repo, dir, file, content, author, msg string) {
	t.Helper()
	path := filepath.Join(dir, file)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := repo.Stage(path); err != nil {
		t.Fatal(err)
	}
	r := repo.r
	w, err := r.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.Commit(msg, &git.CommitOptions{
		Author: &object.Signature{Name: author, Email: author + "@test.com", When: time.Now()},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGitBlame(t *testing.T) {
	dir, repo := initTestRepo(t)
	// sample.txt from init has 3 lines by "tester". Append two more lines in a
	// second commit by another author and blame the result.
	writeAndCommit(t, repo, dir, "sample.txt", "line1\nline2\nline3\nline4\nline5\n", "alex", "second commit")

	blame, err := repo.Blame(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(blame) != 5 {
		t.Fatalf("expected 5 blamed lines, got %d", len(blame))
	}
	for i := 0; i < 3; i++ {
		if blame[i].Author != "tester" {
			t.Fatalf("line %d author = %q, want tester", i+1, blame[i].Author)
		}
		if blame[i].Hash == "" {
			t.Fatalf("line %d missing commit hash", i+1)
		}
	}
	for i := 3; i < 5; i++ {
		if blame[i].Author != "alex" {
			t.Fatalf("line %d author = %q, want alex", i+1, blame[i].Author)
		}
	}
	if blame[0].Hash == blame[3].Hash {
		t.Fatal("different lines should come from different commits here")
	}

	// New untracked file has no commit yet: blame must return an error.
	if _, err := repo.Blame(filepath.Join(dir, "missing.txt")); err == nil {
		t.Fatal("blame of a non-existent file must fail")
	}
}

func TestGitRemoteFetchPush(t *testing.T) {
	dir, repo := initTestRepo(t)
	_ = dir
	if repo.HasRemote() {
		t.Fatal("fresh repo must not report a remote")
	}
	if err := repo.Fetch(); err == nil {
		t.Fatal("fetch without a remote must fail")
	}
	if err := repo.Push(); err == nil {
		t.Fatal("push without a remote must fail")
	}

	// A bare repository acts as "origin" over a local file URL.
	bare := t.TempDir()
	if _, err := git.PlainInit(bare, true); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.r.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{bare}}); err != nil {
		t.Fatal(err)
	}
	if !repo.HasRemote() {
		t.Fatal("repo must report a remote now")
	}

	// Push the current branch (master) to the bare remote.
	if err := repo.Push(); err != nil {
		t.Fatalf("push failed: %v", err)
	}

	// Clone the bare repo, commit, and push so the remote moves forward.
	cloneDir := t.TempDir()
	clone, err := git.PlainClone(cloneDir, false, &git.CloneOptions{URL: bare})
	if err != nil {
		t.Fatalf("clone failed: %v", err)
	}
	w, err := clone.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(cloneDir, "sample.txt")
	if err := os.WriteFile(f, []byte("remote change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Add("sample.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Commit("remote commit", &git.CommitOptions{
		Author: &object.Signature{Name: "remote", Email: "remote@test.com", When: time.Now()},
	}); err != nil {
		t.Fatal(err)
	}
	if err := clone.Push(&git.PushOptions{RemoteName: "origin"}); err != nil {
		t.Fatalf("remote push failed: %v", err)
	}

	// The first repo fetches the updated origin/main.
	if err := repo.Fetch(); err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	remoteRef, err := repo.r.Reference(plumbing.NewRemoteReferenceName("origin", "master"), false)
	if err != nil {
		t.Fatalf("origin/master missing after fetch: %v", err)
	}
	head, err := clone.Head()
	if err != nil {
		t.Fatal(err)
	}
	if remoteRef.Hash() != head.Hash() {
		t.Fatalf("origin/master = %s, want %s", remoteRef.Hash(), head.Hash())
	}
}

// TestStageWithAlternateSpelling reproduces the GitHub Windows-runner failing
// shape without real 8.3 names: go-git exposes the worktree root in its
// resolved form, and a Caller may hold the same directory spelled with a
// different letter case (or a short name). On a naive Rel that grows ".."
// hops, and these go-git's Windows worktree checks reject outright; Stage
// must canonicalize instead. This is the exact failure seen with a %TEMP%
// at C:\Users\RUNNER~1\AppData\Local\Temp.
func TestStageWithAlternateSpelling(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("casing divergence is a Windows path reserve, not a Linux one")
	}
	dir := t.TempDir()
	// A caller's spelling of the same directory with the first path component
	// after the volume flipped in case.
	parts := strings.Split(dir, string(filepath.Separator))
	variant, ok := func() (string, bool) {
		for i := 1; i < len(parts); i++ {
			if alt := strings.ToUpper(parts[i][:1]) + parts[i][1:]; alt != parts[i] {
				parts[i] = alt
				return strings.Join(parts, string(filepath.Separator)), true
			}
		}
		return "", false
	}()
	if !ok || variant == dir {
		t.Skip("could not derive an alternate spelling for this temp dir")
	}
	repo, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(variant, "sample.txt")
	if err := os.WriteFile(f, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Before the canonicalization Stage grew ".." hops and go-git failed the
	// path with "invalid path ... cannot use \"..\"".
	if err := repo.Stage(f); err != nil {
		t.Fatalf("stage across spellings must work: %v", err)
	}
	files, err := repo.StatusFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "sample.txt" {
		t.Fatalf("status = %+v, want exactly one staged sample.txt", files)
	}

	// HEAD content and blame speak the same requirement.
	if _, err := repo.Commit("add sample"); err != nil {
		t.Fatal(err)
	}
	have, err := repo.HeadContent(f)
	if err != nil {
		t.Fatalf("head content across spellings: %v", err)
	}
	if want := "hello\n"; have != want {
		t.Fatalf("head content = %q, want %q", have, want)
	}
	d := repo.DiffBuffer(f, "hello\nbye\n")
	if len(d.Hunks) != 1 {
		t.Fatalf("expected 1 hunk after edit, got %+v", d.Hunks)
	}
}

// agentCommit commits "agent: <msg>" the way agent.Committer does, so tests
// pin the exact commit shape an undo must recognise.
func agentCommit(t *testing.T, repo *Repo, files map[string]string, deleted []string, msg string) {
	t.Helper()
	for name, content := range files {
		full := filepath.Join(repo.Root, filepath.FromSlash(name))
		if d := filepath.Dir(full); d != repo.Root {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := repo.Stage(full); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range deleted {
		full := filepath.Join(repo.Root, filepath.FromSlash(name))
		if err := os.Remove(full); err != nil {
			t.Fatal(err)
		}
		if err := repo.Stage(full); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.Commit("agent: " + msg); err != nil {
		t.Fatal(err)
	}
}

func TestRevertCommitRestoresAgentChanges(t *testing.T) {
	dir, repo := initTestRepo(t)

	// Pre-agent state: an extra file that only exists before the agent runs
	// (the agent deletes it), and a rewritten sample.txt, both committed.
	if err := os.WriteFile(filepath.Join(dir, "gone.txt"), []byte("gone file content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := repo.Stage(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	if err := repo.Stage(filepath.Join(dir, "sample.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Commit("base state"); err != nil {
		t.Fatal(err)
	}

	h, err := repo.LastCommitWithSubject("bogus: ", 50)
	if err != nil {
		t.Fatal(err)
	}
	if !h.IsZero() {
		t.Fatal("no bogus commits must exist in a fresh history")
	}

	// The agent: modified an existing file, created a new one, deleted one.
	agentCommit(t, repo, map[string]string{
		"sample.txt": "agent changed\n",
		"sub/new.go": "package new\n",
	}, []string{"gone.txt"}, "refactor things")

	h, err = repo.LastCommitWithSubject("agent: ", 500)
	if err != nil || h.IsZero() {
		t.Fatalf("agent commit must be findable: h=%s err=%v", h, err)
	}

	paths, err := repo.RevertCommit(h)
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if len(paths) != 3 {
		t.Fatalf("reverted paths = %v, want 3", paths)
	}

	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil || string(got) != "base\n" {
		t.Fatalf("sample.txt after revert = %q (err %v), want base content", got, err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "sub", "new.go")); !os.IsNotExist(statErr) {
		t.Fatalf("agent-created sub/new.go must be removed, stat err=%v", statErr)
	}
	if got2, rerr := os.ReadFile(filepath.Join(dir, "gone.txt")); rerr != nil || string(got2) != "gone file content\n" {
		t.Fatalf("deleted-by-agent file must return: %q err=%v", got2, rerr)
	}
	// The revert is its own commit on top.
	log, lerr := repo.Log(10)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if log[0].Subject != "agent: revert "+h.String()[:7] {
		t.Fatalf("top commit = %q", log[0].Subject)
	}
}

func TestRevertCommitRefusesTouchedFiles(t *testing.T) {
	dir, repo := initTestRepo(t)

	agentCommit(t, repo, map[string]string{"sample.txt": "agent output\n"}, nil, "tweak")

	// The user edits the file afterwards: the revert must refuse in full
	// rather than overwrite newer work.
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("user edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	h, herr := repo.LastCommitWithSubject("agent: ", 500)
	if herr != nil || h.IsZero() {
		t.Fatalf("agent commit must exist: h=%s err=%v", h, herr)
	}
	paths, err := repo.RevertCommit(h)
	if err == nil {
		t.Fatalf("revert must refuse a file edited after the commit, got %v", paths)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if string(got) != "user edited\n" {
		t.Fatalf("refused revert must not touch the file, got %q", got)
	}
}

func TestRevertCommitNoAgentCommits(t *testing.T) {
	_, repo := initTestRepo(t)
	h, err := repo.LastCommitWithSubject("agent: ", 500)
	if err != nil || !h.IsZero() {
		t.Fatalf("no agent commits in history: h=%s err=%v", h, err)
	}
}

