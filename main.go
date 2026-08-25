package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"dmed/internal/editor"
)

// version is set via ldflags: -X main.version=0.3.0
var version = "dev"

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
	p := tea.NewProgram(editor.New(os.Args[1:]...))
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
