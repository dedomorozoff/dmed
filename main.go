package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"dmed/internal/editor"
)

// version is set via ldflags: -X main.version=0.3.0
var version = "0.3.0"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-h", "--help":
			fmt.Println("usage: dmed [dir | files...]")
			return
		case "-v", "--version":
			fmt.Printf("dmed %s\n", version)
			return
		}
	}
	p := tea.NewProgram(editor.New(os.Args[1:]...), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
