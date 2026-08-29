package agent

import (
	"fmt"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"

	"dmed/internal/events"
)

// gitCommitter is the subset of *vcs.Repo the Committer needs, so agents can
// be tested against a fake.
type gitCommitter interface {
	Stage(path string) error
	Commit(msg string) (plumbing.Hash, error)
}

// Committer persists an applied agent series as a single git commit and
// notifies the event bus so open buffers/watchers refresh. This is the
// post-apply step of the diff → review → apply flow.
type Committer struct {
	// Repo is the opened git repository. Nil skips git entirely.
	// A *vcs.Repo satisfies this interface.
	Repo gitCommitter
	// Bus receives EventFileChanged / EventGitUpdated for every committed
	// path (nil disables notifications).
	Bus publisher
}

// Commit stages every changed path and creates one commit whose message is
// derived from the task prompt. It then publishes refresh events for each
// path. A nil repo falls back to publishing events only (no git).
func (c *Committer) Commit(paths []string, prompt string) error {
	if c.Repo != nil {
		for _, p := range paths {
			if err := c.Repo.Stage(p); err != nil {
				return fmt.Errorf("stage %s: %w", p, err)
			}
		}
		msg := "agent: " + firstLine(prompt)
		if _, err := c.Repo.Commit(msg); err != nil {
			return err
		}
	}
	c.notify(paths)
	return nil
}

func (c *Committer) notify(paths []string) {
	if c.Bus == nil {
		return
	}
	for _, p := range paths {
		c.Bus.Publish(events.Event{Type: events.EventFileChanged, Path: p})
		c.Bus.Publish(events.Event{Type: events.EventGitUpdated, Path: p})
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 60 {
		s = s[:60] + "..."
	}
	return s
}
