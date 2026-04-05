package ui

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

const maxDebugLines = 200

type debugLog struct {
	mu     sync.Mutex
	lines  []string
	start  time.Time
	notify func() // called after each log to trigger UI re-render
}

func newDebugLog() *debugLog {
	return &debugLog{start: time.Now()}
}

func (d *debugLog) log(format string, args ...any) {
	d.mu.Lock()
	elapsed := time.Since(d.start)
	line := fmt.Sprintf("[%8.3fs] %s", elapsed.Seconds(), fmt.Sprintf(format, args...))
	d.lines = append(d.lines, line)
	if len(d.lines) > maxDebugLines {
		d.lines = d.lines[len(d.lines)-maxDebugLines:]
	}
	notify := d.notify
	d.mu.Unlock()

	if notify != nil {
		notify()
	}
}

func (d *debugLog) render(width, height int) string {
	d.mu.Lock()
	defer d.mu.Unlock()

	var b strings.Builder
	b.WriteString(titleStyle.Render("Debug Log"))
	b.WriteString("\n\n")

	if len(d.lines) == 0 {
		b.WriteString(dimStyle.Render("No log entries"))
		return b.String()
	}

	// Show most recent lines that fit
	visible := height - 3
	if visible < 1 {
		visible = 1
	}
	start := 0
	if len(d.lines) > visible {
		start = len(d.lines) - visible
	}
	for i := start; i < len(d.lines); i++ {
		line := d.lines[i]
		if len(line) > width-2 {
			line = line[:width-2]
		}
		b.WriteString(dimStyle.Render(line))
		b.WriteString("\n")
	}

	return b.String()
}
