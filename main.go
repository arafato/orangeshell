package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oarafat/orangeshell/internal/config"
	"github.com/oarafat/orangeshell/internal/ui/app"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Force a black default background for the terminal session (OSC 11).
	// This ensures borders, gaps, and all empty areas are black regardless
	// of the user's terminal theme. Restore the original on exit (OSC 111).
	fmt.Fprint(os.Stdout, "\x1b]11;#000000\x1b\\")
	defer fmt.Fprint(os.Stdout, "\x1b]111\x1b\\")

	model := app.NewModel(cfg)

	p := tea.NewProgram(model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running orangeshell: %v\n", err)
		os.Exit(1)
	}
}
