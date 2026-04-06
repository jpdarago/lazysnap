package ui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

func renderStatusBar(width int, archiveCount int, loading bool, errMsg, statusMsg string) string {
	var left string
	if errMsg != "" {
		left = errorStyle.Render(fmt.Sprintf(" %s", errMsg))
	} else if loading {
		left = " Loading..."
	} else if statusMsg != "" {
		left = fmt.Sprintf(" %s", statusMsg)
	} else {
		left = fmt.Sprintf(" %d archives", archiveCount)
	}

	right := " R:refresh c:create r:restore d:del /:filter s:search B:basedir F2:debug "

	leftWidth := lipgloss.Width(left)
	rightWidth := lipgloss.Width(right)

	// Truncate left side if it doesn't fit
	if leftWidth+rightWidth > width {
		maxLeft := width - rightWidth - 3
		if maxLeft > 0 {
			left = " " + left[:maxLeft] + "…"
		} else {
			left = ""
		}
	}

	gap := width - lipgloss.Width(left) - rightWidth
	if gap < 0 {
		gap = 0
	}

	bar := statusBarStyle.Width(width).Render(
		left + lipgloss.NewStyle().Width(gap).Render("") + right,
	)
	return bar
}
