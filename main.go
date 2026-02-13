package main

import (
	"fmt"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/oarafat/orangeshell/internal/config"
	"github.com/oarafat/orangeshell/internal/ui/app"
)

func main() {
	// Ensure Node.js and npm are available (npx ships with npm since v5.2+).
	if _, err := exec.LookPath("npx"); err != nil {
		fmt.Fprintln(os.Stderr, "orangeshell requires Node.js and npm to run.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "  Install Node.js and npm:")
		fmt.Fprintln(os.Stderr, "    https://docs.npmjs.com/downloading-and-installing-node-js-and-npm")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "  Install Wrangler (Cloudflare Workers CLI):")
		fmt.Fprintln(os.Stderr, "    https://developers.cloudflare.com/workers/wrangler/install-and-update/")
		os.Exit(1)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Optional: first positional arg is the directory to scan for projects.
	// Without it, orangeshell skips auto-scan and shows the empty-state menu.
	var scanDir string
	if len(os.Args) > 1 {
		scanDir = os.Args[1]
	}

	// Force a black default background for the terminal session (OSC 11).
	// This ensures borders, gaps, and all empty areas are black regardless
	// of the user's terminal theme. Restore the original on exit (OSC 111).
	fmt.Fprint(os.Stdout, "\x1b]11;#000000\x1b\\")
	defer fmt.Fprint(os.Stdout, "\x1b]111\x1b\\")

	// Initialize the global bubblezone manager for mouse event tracking.
	// zone.Scan() is called in the root View() to register zone positions.
	zone.NewGlobal()

	model := app.NewModel(cfg, scanDir)

	p := tea.NewProgram(model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running orangeshell: %v\n", err)
		os.Exit(1)
	}
}
