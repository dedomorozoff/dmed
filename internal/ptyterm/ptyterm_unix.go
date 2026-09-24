//go:build !windows

package ptyterm

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

func start(opts Options) (*Terminal, error) {
	shell := opts.Command
	if shell == "" {
		shell = os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
	}
	cmd := exec.Command(shell, opts.Args...)
	cmd.Dir = opts.Dir
	cmd.Env = append(os.Environ(), opts.Env...)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(opts.Width), Rows: uint16(opts.Height)})
	if err != nil {
		return nil, err
	}
	return &Terminal{
		rw: f,
		resize: func(cols, rows int) error {
			return pty.Setsize(f, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
		},
		wait: cmd.Wait,
		close: func() error {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			return f.Close()
		},
	}, nil
}
