package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jpdarago/lazysnap/internal/cache"
	"github.com/jpdarago/lazysnap/internal/tarsnap"
	"github.com/jpdarago/lazysnap/internal/ui"
)

func main() {
	keyfile := flag.String("keyfile", "", "path to tarsnap key file (optional, defaults to tarsnaprc)")
	timeout := flag.Duration("timeout", 2*time.Minute, "timeout for tarsnap commands (0 to disable)")
	flag.Parse()

	db, err := cache.Open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening cache: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	tc := tarsnap.NewClient()
	tc.Keyfile = *keyfile
	tc.Timeout = *timeout
	model := ui.NewModel(tc, db)

	p := tea.NewProgram(model, tea.WithAltScreen())
	model.NotifyFromDebugLog(p)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
