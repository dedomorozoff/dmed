package vcs

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// DiffType indicates the Git change status of a line in the buffer.
type DiffType int

const (
	DiffNone DiffType = iota
	DiffAdded
	DiffModified
	DiffDeleted
)

// Hunk represents a contiguous range of changed lines.
type Hunk struct {
	StartLine int
	EndLine   int
	Type      DiffType
}

// FileDiff contains line-by-line diff statuses and hunk ranges.
type FileDiff struct {
	Lines []DiffType
	Hunks []Hunk
}

// Repo wraps a Git repository and provides VCS queries and operations.
type Repo struct {
	Root string
	r    *git.Repository
}

// Open finds and opens the Git repository containing path.
func Open(path string) (*Repo, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}

	r, err := git.PlainOpenWithOptions(abs, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil, err
	}

	wt, err := r.Worktree()
	if err != nil {
		return nil, err
	}

	return &Repo{
		Root: wt.Filesystem.Root(),
		r:    r,
	}, nil
}

// Branch returns the current branch name or short SHA.
func (repo *Repo) Branch() string {
	head, err := repo.r.Head()
	if err != nil {
		return ""
	}
	if head.Name().IsBranch() {
		return head.Name().Short()
	}
	h := head.Hash().String()
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

// StatusSummary returns a short string summarizing repository changes.
func (repo *Repo) StatusSummary() string {
	wt, err := repo.r.Worktree()
	if err != nil {
		return ""
	}
	st, err := wt.Status()
	if err != nil {
		return ""
	}

	modified := 0
	added := 0
	deleted := 0

	for _, fs := range st {
		if fs.Worktree == git.Modified || fs.Staging == git.Modified {
			modified++
		} else if fs.Worktree == git.Untracked || fs.Staging == git.Added {
			added++
		} else if fs.Worktree == git.Deleted || fs.Staging == git.Deleted {
			deleted++
		}
	}

	var parts []string
	if added > 0 {
		parts = append(parts, fmt.Sprintf("+%d", added))
	}
	if modified > 0 {
		parts = append(parts, fmt.Sprintf("~%d", modified))
	}
	if deleted > 0 {
		parts = append(parts, fmt.Sprintf("-%d", deleted))
	}
	if len(parts) == 0 {
		return "clean"
	}
	return strings.Join(parts, " ")
}

// HeadContent returns the content of the file at HEAD.
func (repo *Repo) HeadContent(absPath string) (string, error) {
	rel, err := filepath.Rel(repo.Root, absPath)
	if err != nil {
		return "", err
	}
	rel = filepath.ToSlash(rel)

	head, err := repo.r.Head()
	if err != nil {
		return "", err
	}

	commit, err := repo.r.CommitObject(head.Hash())
	if err != nil {
		return "", err
	}

	file, err := commit.File(rel)
	if err != nil {
		return "", err
	}

	reader, err := file.Reader()
	if err != nil {
		return "", err
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// DiffBuffer computes line-by-line diff and hunks for buffer text against HEAD.
func (repo *Repo) DiffBuffer(absPath, bufText string) FileDiff {
	headText, err := repo.HeadContent(absPath)
	if err != nil {
		// New file not in HEAD: all lines are added
		lines := strings.Split(bufText, "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		diffLines := make([]DiffType, len(lines))
		for i := range diffLines {
			diffLines[i] = DiffAdded
		}
		var hunks []Hunk
		if len(lines) > 0 {
			hunks = append(hunks, Hunk{StartLine: 0, EndLine: len(lines) - 1, Type: DiffAdded})
		}
		return FileDiff{Lines: diffLines, Hunks: hunks}
	}

	dmp := diffmatchpatch.New()
	a, b, lineArray := dmp.DiffLinesToRunes(headText, bufText)
	diffs := dmp.DiffMainRunes(a, b, false)
	diffs = dmp.DiffCharsToLines(diffs, lineArray)

	var resLines []DiffType
	hasPrevDelete := false

	for i := 0; i < len(diffs); i++ {
		d := diffs[i]
		count := strings.Count(d.Text, "\n")
		if count == 0 && len(d.Text) > 0 {
			count = 1
		}

		switch d.Type {
		case diffmatchpatch.DiffEqual:
			for k := 0; k < count; k++ {
				resLines = append(resLines, DiffNone)
			}
			hasPrevDelete = false
		case diffmatchpatch.DiffDelete:
			hasPrevDelete = true
		case diffmatchpatch.DiffInsert:
			dt := DiffAdded
			if hasPrevDelete {
				dt = DiffModified
			}
			for k := 0; k < count; k++ {
				resLines = append(resLines, dt)
			}
			hasPrevDelete = false
		}
	}

	// Compute hunks
	var hunks []Hunk
	inHunk := false
	hunkStart := 0
	hunkType := DiffNone

	for i, dt := range resLines {
		if dt != DiffNone {
			if !inHunk {
				inHunk = true
				hunkStart = i
				hunkType = dt
			} else if dt != hunkType {
				hunks = append(hunks, Hunk{StartLine: hunkStart, EndLine: i - 1, Type: hunkType})
				hunkStart = i
				hunkType = dt
			}
		} else {
			if inHunk {
				hunks = append(hunks, Hunk{StartLine: hunkStart, EndLine: i - 1, Type: hunkType})
				inHunk = false
			}
		}
	}
	if inHunk {
		hunks = append(hunks, Hunk{StartLine: hunkStart, EndLine: len(resLines) - 1, Type: hunkType})
	}

	return FileDiff{Lines: resLines, Hunks: hunks}
}

// Stage stages a file path relative to repo root or absolute.
func (repo *Repo) Stage(path string) error {
	wt, err := repo.r.Worktree()
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(repo.Root, path)
	if err != nil {
		rel = path
	}
	rel = filepath.ToSlash(rel)
	_, err = wt.Add(rel)
	return err
}

// Commit creates a commit with the given message.
func (repo *Repo) Commit(msg string) (plumbing.Hash, error) {
	wt, err := repo.r.Worktree()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	return wt.Commit(msg, &git.CommitOptions{
		Author: &object.Signature{
			Name:  "dmed",
			Email: "dmed@local",
			When:  time.Now(),
		},
	})
}
