package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"dmed/internal/editor"
)

func main() {
	if len(os.Args) > 2 {
		fmt.Fprintln(os.Stderr, "usage: dmed [file]")
		os.Exit(2)
	}
	path := ""
	if len(os.Args) == 2 {
		path = os.Args[1]
	}
	p := tea.NewProgram(editor.New(path), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
