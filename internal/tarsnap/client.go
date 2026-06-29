package tarsnap

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// LogFunc is a callback for debug logging.
type LogFunc func(format string, args ...any)

// Client wraps the tarsnap CLI.
type Client struct {
	Binary     string        // path to tarsnap binary, defaults to "tarsnap"
	Keyfile    string        // optional path to tarsnap key file
	Passphrase string        // passphrase for encrypted key files
	Timeout    time.Duration // command timeout, 0 means no timeout
	Log        LogFunc
}

// NewClient creates a new tarsnap client.
func NewClient() *Client {
	return &Client{Binary: "tarsnap"}
}

func (c *Client) log(format string, args ...any) {
	if c.Log != nil {
		c.Log(format, args...)
	}
}

// ListArchives returns all archives from tarsnap.
func (c *Client) ListArchives() ([]Archive, error) {
	res, err := c.run("--list-archives", "-vv")
	if err != nil {
		return nil, fmt.Errorf("list archives: %w", err)
	}

	var archives []Archive
	scanner := bufio.NewScanner(bytes.NewReader(res.stdout))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		name, dateStr := parseArchiveLine(line)
		if name == "" {
			continue
		}
		t, err := time.Parse("2006-01-02 15:04:05", dateStr)
		if err != nil {
			continue
		}
		archives = append(archives, Archive{Name: name, CreatedAt: t})
	}
	return archives, scanner.Err()
}

// ArchiveStats returns size statistics for an archive.
func (c *Client) ArchiveStats(archiveName string) (*ArchiveStats, error) {
	res, err := c.run("--print-stats", "-f", archiveName)
	if err != nil {
		return nil, fmt.Errorf("archive stats: %w", err)
	}

	// tarsnap prints stats to stderr (some versions use stdout).
	// Output format:
	//                                        Total size  Compressed size
	// All archives                           5944711448       5887260696
	//   (unique data)                         943134574        934524056
	// <archive-name>                           42333678         43146270
	//   (unique data)                             27118            25430
	//
	// We need the archive-specific lines (after "All archives" section).
	stats := &ArchiveStats{ArchiveName: archiveName}
	combined := append(res.stderr, res.stdout...)
	scanner := bufio.NewScanner(bytes.NewReader(combined))
	foundArchive := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		c.log("stats parse: %q", line)
		if strings.HasPrefix(line, archiveName) {
			stats.TotalSize, stats.CompressedSize = parseSizeColumns(line)
			foundArchive = true
			c.log("stats parse: archive -> total=%d compressed=%d", stats.TotalSize, stats.CompressedSize)
		} else if foundArchive && strings.HasPrefix(line, "(unique data)") {
			stats.UniqueSize, stats.UniqueCompSize = parseSizeColumns(line)
			c.log("stats parse: unique -> size=%d compressed=%d", stats.UniqueSize, stats.UniqueCompSize)
		}
	}
	if stats.TotalSize == 0 && stats.UniqueSize == 0 {
		c.log("stats parse: WARNING no stats found in output (stderr=%d bytes, stdout=%d bytes)", len(res.stderr), len(res.stdout))
	}
	return stats, scanner.Err()
}

// ListFiles returns the file listing for an archive.
func (c *Client) ListFiles(archiveName string) ([]FileEntry, error) {
	res, err := c.run("-tf", archiveName)
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}

	var entries []FileEntry
	scanner := bufio.NewScanner(bytes.NewReader(res.stdout))
	for scanner.Scan() {
		path := scanner.Text()
		if path == "" {
			continue
		}
		entry := FileEntry{
			Path:  path,
			IsDir: strings.HasSuffix(path, "/"),
		}
		entries = append(entries, entry)
	}
	return entries, scanner.Err()
}

// CreateArchive creates a new archive with the given name and paths.
func (c *Client) CreateArchive(name string, paths []string) error {
	args := append([]string{"-c", "-f", name}, paths...)
	_, err := c.run(args...)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	return nil
}

// DeleteArchive deletes an archive.
func (c *Client) DeleteArchive(name string) error {
	_, err := c.run("-d", "-f", name)
	if err != nil {
		return fmt.Errorf("delete archive: %w", err)
	}
	return nil
}

// RestoreProgress holds a progress update from a restore operation.
type RestoreProgress struct {
	Line string // raw progress line from tarsnap
}

// Restore extracts files from an archive to the given directory.
func (c *Client) Restore(archiveName string, targetDir string) error {
	return c.RestoreWithProgress(archiveName, targetDir, nil)
}

// RestoreWithProgress extracts files and reports progress via the callback.
func (c *Client) RestoreWithProgress(archiveName, targetDir string, onProgress func(RestoreProgress)) error {
	args := []string{"-x", "-f", archiveName, "-C", targetDir, "--progress-bytes", "1M"}
	if c.Keyfile != "" {
		args = append([]string{"--keyfile", c.Keyfile}, args...)
	}
	if c.Passphrase != "" {
		args = append([]string{"--passphrase", "dev:stdin-once"}, args...)
	}
	c.log("exec: %s %s", c.Binary, strings.Join(args, " "))

	var cmd *exec.Cmd
	var cancel context.CancelFunc
	if c.Timeout > 0 {
		ctx, cf := context.WithTimeout(context.Background(), c.Timeout)
		cancel = cf
		cmd = exec.CommandContext(ctx, c.Binary, args...)
	} else {
		cmd = exec.Command(c.Binary, args...)
	}
	if cancel != nil {
		defer cancel()
	}

	if c.Passphrase != "" {
		cmd.Stdin = strings.NewReader(c.Passphrase + "\n")
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}
	cmd.Stdout = nil

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case t := <-ticker.C:
				elapsed := t.Sub(start).Truncate(time.Second)
				c.log("restore: still waiting... (%s elapsed)", elapsed)
				if onProgress != nil {
					onProgress(RestoreProgress{Line: fmt.Sprintf("Restoring... %s elapsed", elapsed)})
				}
			}
		}
	}()

	var stderrBuf bytes.Buffer
	scanner := bufio.NewScanner(stderrPipe)
	// Tarsnap progress uses \r for in-place updates; split on both \r and \n.
	scanner.Split(scanLinesOrCR)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		stderrBuf.WriteString(line)
		stderrBuf.WriteByte('\n')
		c.log("stderr: %s", line)
		if onProgress != nil {
			onProgress(RestoreProgress{Line: line})
		}
	}

	err = cmd.Wait()
	close(done)
	elapsed := time.Since(start)
	if err != nil {
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			c.log("restore killed after %s (timeout or signal)", elapsed)
			return fmt.Errorf("restore timed out after %s", elapsed)
		}
		c.log("restore failed after %s: %v: %s", elapsed, err, stderrBuf.String())
		return fmt.Errorf("restore: %w: %s", err, stderrBuf.String())
	}
	c.log("restore completed in %s", elapsed)
	return nil
}

// scanLinesOrCR is a bufio.SplitFunc that splits on \n, \r\n, or \r.
// This handles tarsnap's progress output which uses \r for in-place updates.
func scanLinesOrCR(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i, b := range data {
		if b == '\n' {
			// \r\n
			if i > 0 && data[i-1] == '\r' {
				return i + 1, data[:i-1], nil
			}
			return i + 1, data[:i], nil
		}
		if b == '\r' {
			// Check if next byte is \n — if so, let the \n case handle it.
			if i+1 < len(data) {
				if data[i+1] == '\n' {
					continue
				}
				return i + 1, data[:i], nil
			}
			// At end of buffer, need more data to decide.
			if !atEOF {
				return 0, nil, nil
			}
			return i + 1, data[:i], nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// Fsck runs tarsnap --fsck.
func (c *Client) Fsck() error {
	_, err := c.run("--fsck")
	if err != nil {
		return fmt.Errorf("fsck: %w", err)
	}
	return nil
}

type runResult struct {
	stdout []byte
	stderr []byte
}

func (c *Client) run(args ...string) (*runResult, error) {
	if c.Keyfile != "" {
		args = append([]string{"--keyfile", c.Keyfile}, args...)
	}
	if c.Passphrase != "" {
		args = append([]string{"--passphrase", "dev:stdin-once"}, args...)
	}
	c.log("exec: %s %s", c.Binary, strings.Join(args, " "))

	var cmd *exec.Cmd
	var cancel context.CancelFunc
	if c.Timeout > 0 {
		ctx, cf := context.WithTimeout(context.Background(), c.Timeout)
		cancel = cf
		cmd = exec.CommandContext(ctx, c.Binary, args...)
	} else {
		cmd = exec.Command(c.Binary, args...)
	}
	if cancel != nil {
		defer cancel()
	}

	if c.Passphrase != "" {
		cmd.Stdin = strings.NewReader(c.Passphrase + "\n")
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	// Log a heartbeat every 5s so the user knows we're still waiting.
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case t := <-ticker.C:
				c.log("still waiting... (%s elapsed)", t.Sub(start).Truncate(time.Second))
			}
		}
	}()

	// Stream stderr lines to debug log as they arrive.
	var stderrBuf bytes.Buffer
	scanner := bufio.NewScanner(stderrPipe)
	for scanner.Scan() {
		line := scanner.Text()
		stderrBuf.WriteString(line)
		stderrBuf.WriteByte('\n')
		c.log("stderr: %s", line)
	}

	err = cmd.Wait()
	close(done)
	elapsed := time.Since(start)
	if err != nil {
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			c.log("exec killed after %s (timeout or signal)", elapsed)
			return nil, fmt.Errorf("command timed out after %s", elapsed)
		}
		c.log("exec failed after %s: %v: %s", elapsed, err, stderrBuf.String())
		return nil, fmt.Errorf("%w: %s", err, stderrBuf.String())
	}
	c.log("exec completed in %s (stdout=%d bytes, stderr=%d bytes)", elapsed, stdout.Len(), stderrBuf.Len())
	for label, buf := range map[string]string{"stdout": stdout.String(), "stderr": stderrBuf.String()} {
		lines := strings.SplitN(buf, "\n", 21)
		for i, line := range lines {
			if i >= 20 {
				c.log("%s: ... (%d more lines)", label, strings.Count(buf, "\n")-20)
				break
			}
			if line != "" {
				c.log("%s: %s", label, line)
			}
		}
	}
	return &runResult{stdout: stdout.Bytes(), stderr: stderrBuf.Bytes()}, nil
}

// parseArchiveLine extracts archive name and date from a tarsnap -vv line.
// Handles both tab-separated and multi-space-separated formats.
func parseArchiveLine(line string) (name, dateStr string) {
	// Try tab-separated first
	parts := strings.SplitN(line, "\t", 3)
	if len(parts) >= 2 {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	// Fall back to finding a date pattern (YYYY-MM-DD HH:MM:SS) in the line
	for i := 0; i <= len(line)-19; i++ {
		candidate := line[i : i+19]
		if _, err := time.Parse("2006-01-02 15:04:05", candidate); err == nil {
			return strings.TrimSpace(line[:i]), candidate
		}
	}
	return "", ""
}

// parseSizeColumns extracts two numeric columns from a stats line.
func parseSizeColumns(line string) (int64, int64) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0, 0
	}
	a, _ := strconv.ParseInt(fields[len(fields)-2], 10, 64)
	b, _ := strconv.ParseInt(fields[len(fields)-1], 10, 64)
	return a, b
}
