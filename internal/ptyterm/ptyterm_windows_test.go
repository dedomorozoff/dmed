//go:build windows

package ptyterm

import "os"

func testOptions() Options {
	shell := os.Getenv("COMSPEC")
	if shell == "" {
		shell = "cmd.exe"
	}
	return Options{Command: shell, Args: []string{"/C", "echo dmed-pty-ok"}, Width: 80, Height: 24}
}
