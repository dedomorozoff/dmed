//go:build !windows

package ptyterm

func testOptions() Options {
	return Options{Command: "/bin/sh", Args: []string{"-c", "printf dmed-pty-ok"}, Width: 80, Height: 24}
}
