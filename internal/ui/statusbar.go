package ui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

func renderStatusBar(width int, archiveCount int, loading bool, errMsg string) string {
	var left string
	if errMsg != "" {
		left = errorStyle.Render(fmt.Sprintf(" %s", errMsg))
	} else if loading {
		left = " Loading..."
	} else {
		left = fmt.Sprintf(" %d archives", archiveCount)
	}

	right := " q:quit  R:refresh  d:delete  /:filter  F2/ctrl-d:debug "

	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 0 {
		gap = 0
	}

	bar := statusBarStyle.Width(width).Render(
		left + lipgloss.NewStyle().Width(gap).Render("") + right,
	)
	return bar
}
