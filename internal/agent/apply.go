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
			return os.WriteFile(filepath.FromSlash(p), []byte(content), 0o644)
		},
	}
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
		cur, err := a.Read(c.Path)
		if err != nil {
			return fmt.Errorf("change %d (%s): cannot read current content: %w", i, c.Path, err)
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

		orig, err := a.Read(c.Path)
		if err != nil {
			a.rollback(done)
			return fmt.Errorf("change %d (%s): read for rollback failed: %w", i, c.Path, err)
		}
		if err := a.Write(c.Path, c.New); err != nil {
			a.rollback(done)
			return fmt.Errorf("change %d (%s): write failed: %w", i, c.Path, err)
		}
		done = append(done, written{path: c.Path, orig: orig})
	}
	return nil
}

// written tracks a file that was overwritten so it can be restored on rollback.
type written struct {
	path string
	orig string
}

// rollback restores previously written files to their original bytes, best-effort.
func (a *Applier) rollback(done []written) {
	for i := len(done) - 1; i >= 0; i-- {
		_ = a.Write(done[i].path, done[i].orig)
	}
}

// normalize trims surrounding whitespace for a tolerant comparison so minor
// trailing-newline differences don't reject a valid patch.
func normalize(s string) string {
	return strings.TrimSpace(s)
}
