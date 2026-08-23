package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"dmed/internal/editor"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help") {
		fmt.Println("usage: dmed [dir | files...]")
		return
	}
	p := tea.NewProgram(editor.New(os.Args[1:]...), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
