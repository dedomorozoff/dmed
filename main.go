package main

import (
	"fmt"
	"os"
	"runtime/debug"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/editor"
	"dmed/internal/setup"
)

// version is set via ldflags: -X main.version=0.3.0
var version = "dev"

// run holds the editor lifetime. It is separated from main so a panic in the
// Bubble Tea update/render loop is reported with a stack trace instead of
// taking the whole process down with a bare exit status 2 — the editor is a
// long-running TUI holding unsaved buffers, so losing it silently (or
// printing nothing actionable) is the worst possible failure mode.
// Panics in background goroutines are already caught by
// internal/debug.CapturePanicReport; this covers the ones on this goroutine.
func run() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("internal error: %v\n%s", r, debug.Stack())
		}
	}()
	model := editor.New(os.Args[1:]...)
	model.ApplyTerminalCompat()
	model.Version = version
	// Ctrl+C must never kill the editor: in a terminal it arrives as a key
	// (the editor copies the selection and says how to quit), and when the
	// input is not a terminal it arrives as SIGINT, which bubbletea would
	// turn into an interrupt. SIGTERM still stops the editor.
	p := tea.NewProgram(model, tea.WithFilter(func(_ tea.Model, msg tea.Msg) tea.Msg {
		if _, ok := msg.(tea.InterruptMsg); ok {
			return nil
		}
		return msg
	}))
	_, err = p.Run()
	return err
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-h", "--help":
			fmt.Println("usage: dmed [dir | files...]")
			fmt.Println("       dmed setup-ai    interactive AI provider setup (writes ~/.dmed.conf)")
			return
		case "-v", "--version":
			fmt.Printf("dmed %s\n", version)
			return
		case "setup-ai":
			if err := setup.Run(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
