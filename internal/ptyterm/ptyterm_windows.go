//go:build windows

package ptyterm

import (
	"context"
	"os"
	"strings"

	"github.com/UserExistsError/conpty"
)

func start(opts Options) (*Terminal, error) {
	shell := opts.Command
	if shell == "" {
		shell = os.Getenv("COMSPEC")
		if shell == "" {
			shell = "cmd.exe"
		}
	}
	command := quoteWindows(shell)
	for _, arg := range opts.Args {
		command += " " + quoteWindows(arg)
	}
	cpty, err := conpty.Start(command,
		conpty.ConPtyDimensions(opts.Width, opts.Height),
		conpty.ConPtyWorkDir(opts.Dir),
		conpty.ConPtyEnv(append(os.Environ(), opts.Env...)),
	)
	if err != nil {
		return nil, err
	}
	return &Terminal{
		rw: cpty, resize: cpty.Resize,
		wait:  func() error { _, err := cpty.Wait(context.Background()); return err },
		close: cpty.Close,
	}, nil
}

// quoteWindows applies the CommandLineToArgvW quoting rules for a single token.
func quoteWindows(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for _, r := range s {
		if r == '\\' {
			slashes++
			continue
		}
		if r == '"' {
			b.WriteString(strings.Repeat("\\", slashes*2+1))
		} else {
			b.WriteString(strings.Repeat("\\", slashes))
		}
		slashes = 0
		b.WriteRune(r)
	}
	b.WriteString(strings.Repeat("\\", slashes*2))
	b.WriteByte('"')
	return b.String()
}
