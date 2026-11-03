// Package ptyterm provides a small cross-platform pseudo-terminal transport.
// Windows uses ConPTY; Unix-like systems use a PTY allocated by creack/pty.
package ptyterm

import (
	"io"
	"os"
	"strings"
	"sync"
)

type Options struct {
	Command string   // executable path; empty selects the platform default shell
	Args    []string // arguments passed to Command
	Dir     string   // working directory; empty is inherited
	Env     []string // extra KEY=value entries, winning over the inherited ones
	Width   int      // initial terminal columns
	Height  int      // initial terminal rows
}

// mergeEnv returns the environment the child is given: the parent's, with the
// caller's entries applied over it. Order matters more than it looks. Windows
// resolves a repeated name to the FIRST entry of the block handed to
// CreateProcess, so simply appending HOME=<dir> to os.Environ() left the
// parent's HOME in force and every later override a silent no-op. os/exec
// dedupes on Unix by itself, but both platforms are built from this list so the
// two mean exactly the same thing.
func mergeEnv(extra []string) []string {
	out := make([]string, 0, len(os.Environ())+len(extra))
	at := make(map[string]int, cap(out))
	add := func(kv string) {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			return
		}
		if envFoldKeys {
			name = strings.ToUpper(name)
		}
		if i, seen := at[name]; seen {
			out[i] = kv
			return
		}
		at[name] = len(out)
		out = append(out, kv)
	}
	for _, kv := range os.Environ() {
		add(kv)
	}
	for _, kv := range extra {
		add(kv)
	}
	return out
}

// Terminal is a live pseudo-terminal session.
type Terminal struct {
	rw       io.ReadWriteCloser
	resize   func(int, int) error
	wait     func() error
	close    func() error
	once     sync.Once
	closeErr error
}

func Start(opts Options) (*Terminal, error) {
	if opts.Width <= 0 {
		opts.Width = 80
	}
	if opts.Height <= 0 {
		opts.Height = 24
	}
	return start(opts)
}

func (t *Terminal) Read(p []byte) (int, error) {
	if t == nil || t.rw == nil {
		return 0, os.ErrClosed
	}
	return t.rw.Read(p)
}

func (t *Terminal) Write(p []byte) (int, error) {
	if t == nil || t.rw == nil {
		return 0, os.ErrClosed
	}
	return t.rw.Write(p)
}

func (t *Terminal) Resize(cols, rows int) error {
	if t == nil || t.resize == nil || cols <= 0 || rows <= 0 {
		return nil
	}
	return t.resize(cols, rows)
}

func (t *Terminal) Wait() error {
	if t == nil || t.wait == nil {
		return nil
	}
	return t.wait()
}

func (t *Terminal) Close() error {
	if t == nil {
		return nil
	}
	t.once.Do(func() {
		if t.close != nil {
			t.closeErr = t.close()
		}
	})
	return t.closeErr
}

var _ io.ReadWriteCloser = (*Terminal)(nil)
