package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Applier applies a series of Changes atomically: either every change lands
// or none does. Validation happens up front (stale/conflicting patches reject
// the whole series before any write); if a write fails partway, earlier
// writes are rolled back to their original bytes.
//
// This is the core of the project rule: agent edits never touch buffers or
// files directly, they land through this all-or-nothing apply step.
type Applier struct {
	// Read returns the current content of a file (used for validation and
	// for capturing rollback bytes). Defaults to os.ReadFile.
	Read func(path string) (string, error)
	// Write persists content to a file. Defaults to os.WriteFile.
	Write func(path, content string) error
	// Remove deletes a file (used to roll back newly created ones). Defaults
	// to os.Remove.
	Remove func(path string) error
}

// NewApplier returns an Applier using the OS filesystem.
func NewApplier() *Applier {
	return &Applier{
		Read: func(p string) (string, error) {
			b, err := os.ReadFile(filepath.FromSlash(p))
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
		Write: func(p, content string) error {
			p = filepath.FromSlash(p)
			if dir := filepath.Dir(p); dir != "" && dir != "." {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return err
				}
			}
			return os.WriteFile(p, []byte(content), 0o644)
		},
		Remove: func(p string) error {
			return os.Remove(filepath.FromSlash(p))
		},
	}
}

// readCurrent returns the file content for validation/rollback. A missing file
// counts as empty content, which makes a Change with an empty Orig a file
// creation instead of an error.
func (a *Applier) readCurrent(path string) (string, bool, error) {
	cur, err := a.Read(path)
	if err != nil {
		if isNotExistErr(err) {
			return "", false, nil // missing on disk: creation
		}
		return "", false, err
	}
	return cur, true, nil
}

func isNotExistErr(err error) bool {
	if err == nil {
		return false
	}
	return os.IsNotExist(err) || strings.Contains(err.Error(), "cannot find the file") ||
		strings.Contains(err.Error(), "cannot find the path")
}

// Validate checks that every change still applies cleanly against the current
// on-disk content. A change whose current content differs from Change.Orig is
// stale (the file moved on under the agent) and invalidates the whole series.
func (a *Applier) Validate(changes []Change) error {
	for i := range changes {
		c := &changes[i]
		if c.Path == "" {
			return fmt.Errorf("change %d: empty path", i)
		}
		cur, exists, err := a.readCurrent(c.Path)
		if err != nil {
			return fmt.Errorf("change %d (%s): cannot read current content: %w", i, c.Path, err)
		}
		if !exists && normalize(c.Orig) != "" {
			return fmt.Errorf("change %d (%s): file does not exist but the patch carries original content; refusing to apply", i, c.Path)
		}
		if normalize(cur) != normalize(c.Orig) {
			return fmt.Errorf("change %d (%s): file content no longer matches agent's snapshot; refusing to apply (stale patch)", i, c.Path)
		}
	}
	return nil
}

// Apply validates the whole series and, if it is clean, writes every change.
// On a partial write failure it rolls back previously written files and
// returns the first error. On success it returns nil (callers should then
// trigger a git commit / event separately).
func (a *Applier) Apply(changes []Change) error {
	if err := a.Validate(changes); err != nil {
		return err
	}

	var done []written

	for i := range changes {
		c := &changes[i]

		orig, exists, err := a.readCurrent(c.Path)
		if err != nil {
			a.rollback(done)
			return fmt.Errorf("change %d (%s): read for rollback failed: %w", i, c.Path, err)
		}
		if err := a.Write(c.Path, c.New); err != nil {
			a.rollback(done)
			return fmt.Errorf("change %d (%s): write failed: %w", i, c.Path, err)
		}
		done = append(done, written{path: c.Path, orig: orig, created: !exists})
	}
	return nil
}

// written tracks a file that was overwritten so it can be restored on rollback.
type written struct {
	path    string
	orig    string
	created bool // the file did not exist before: roll back by deleting it
}

// rollback restores previously written files to their original bytes, best-effort.
// Files created by the series are deleted again.
func (a *Applier) rollback(done []written) {
	for i := len(done) - 1; i >= 0; i-- {
		if done[i].created {
			if a.Remove != nil {
				_ = a.Remove(done[i].path)
			} else {
				_ = os.Remove(filepath.FromSlash(done[i].path))
			}
			continue
		}
		_ = a.Write(done[i].path, done[i].orig)
	}
}

// normalize trims surrounding whitespace for a tolerant comparison so minor
// trailing-newline differences don't reject a valid patch.
func normalize(s string) string {
	return strings.TrimSpace(s)
}
