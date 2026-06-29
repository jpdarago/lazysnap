// Package setup implements the `lazysnap init` command, which bootstraps
// tarsnap on a fresh machine from an existing key (e.g. one restored from a
// password manager). It deliberately knows nothing about any specific password
// manager: the key is read from stdin or a file, never fetched directly.
package setup

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// keyMarker appears in every tarsnap key file; we use it as a sanity check so a
// truncated paste or the wrong clipboard contents fail loudly instead of
// producing a broken config.
const keyMarker = "TARSNAP KEY FILE"

// options holds the resolved configuration for an init run.
type options struct {
	keyFile  string // source key path; "" means read from stdin
	keyOut   string // destination key path
	cacheDir string // tarsnap cache directory
	rcPath   string // path to the generated tarsnaprc
	tarsnap  string // tarsnap binary
	force    bool   // overwrite existing key/config
	runFsck  bool   // run tarsnap --fsck after writing config
	stdin    io.Reader
	stdinTTY bool
	stderr   io.Writer
}

// Init runs the `lazysnap init` subcommand. args are the arguments after the
// subcommand name (i.e. os.Args[2:]).
func Init(args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}

	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	keyFile := fs.String("key-file", "", "read the tarsnap key from this file instead of stdin")
	keyOut := fs.String("key-out", filepath.Join(home, ".tarsnap.key"), "where to write the tarsnap key")
	cacheDir := fs.String("cachedir", filepath.Join(home, ".tarsnap-cache"), "tarsnap cache directory")
	rcPath := fs.String("rc", filepath.Join(home, ".tarsnaprc"), "path to the tarsnap config file to generate")
	tarsnapBin := fs.String("tarsnap", "tarsnap", "path to the tarsnap binary")
	force := fs.Bool("force", false, "overwrite an existing key or config file")
	noFsck := fs.Bool("no-fsck", false, "skip the 'tarsnap --fsck' cache rebuild")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, usage)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	opts := options{
		keyFile:  *keyFile,
		keyOut:   *keyOut,
		cacheDir: *cacheDir,
		rcPath:   *rcPath,
		tarsnap:  *tarsnapBin,
		force:    *force,
		runFsck:  !*noFsck,
		stdin:    os.Stdin,
		stdinTTY: isTerminal(os.Stdin),
		stderr:   os.Stderr,
	}
	return run(opts)
}

func run(o options) error {
	// 1. Refuse to clobber unless --force, before reading anything.
	if !o.force {
		for _, p := range []string{o.keyOut, o.rcPath} {
			if _, err := os.Stat(p); err == nil {
				return fmt.Errorf("%s already exists; re-run with --force to overwrite", p)
			}
		}
	}

	// 2. Read the key from a file or stdin.
	key, err := readKey(o)
	if err != nil {
		return err
	}
	if err := validateKey(key); err != nil {
		return err
	}

	// 3. Write the key with owner-only permissions.
	if err := os.MkdirAll(filepath.Dir(o.keyOut), 0o700); err != nil {
		return fmt.Errorf("create key directory: %w", err)
	}
	if err := writeFile(o.keyOut, key, 0o600); err != nil {
		return fmt.Errorf("write key: %w", err)
	}
	fmt.Fprintf(o.stderr, "Wrote key to %s\n", o.keyOut)

	// 4. Create the cache directory.
	if err := os.MkdirAll(o.cacheDir, 0o700); err != nil {
		return fmt.Errorf("create cache directory: %w", err)
	}

	// 5. Generate the tarsnaprc with absolute paths (tarsnap does not expand ~).
	rc := renderTarsnaprc(o.keyOut, o.cacheDir)
	if err := writeFile(o.rcPath, []byte(rc), 0o644); err != nil {
		return fmt.Errorf("write tarsnaprc: %w", err)
	}
	fmt.Fprintf(o.stderr, "Wrote config to %s\n", o.rcPath)

	// 6. Rebuild the local cache from the server so listings work immediately.
	if o.runFsck {
		fmt.Fprintf(o.stderr, "Rebuilding tarsnap cache (this may take a moment)...\n")
		cmd := exec.Command(o.tarsnap, "--fsck")
		cmd.Stdout = o.stderr
		cmd.Stderr = o.stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("tarsnap --fsck failed (key and config were written; "+
				"fix the issue and re-run 'tarsnap --fsck'): %w", err)
		}
	}

	fmt.Fprintf(o.stderr, "\ntarsnap is ready. Run 'lazysnap' to browse your archives.\n")
	return nil
}

// readKey returns the key bytes from the configured source.
func readKey(o options) ([]byte, error) {
	if o.keyFile != "" {
		b, err := os.ReadFile(o.keyFile)
		if err != nil {
			return nil, fmt.Errorf("read key file: %w", err)
		}
		return b, nil
	}
	if o.stdinTTY {
		fmt.Fprintf(o.stderr, "Paste your tarsnap key, then press Ctrl-D:\n")
	}
	b, err := io.ReadAll(o.stdin)
	if err != nil {
		return nil, fmt.Errorf("read key from stdin: %w", err)
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, fmt.Errorf("no key provided on stdin (use --key-file, or pipe/paste the key)")
	}
	return b, nil
}

// isTerminal reports whether r is a character device (an interactive terminal)
// rather than a pipe or regular file. Used to decide whether to print a paste
// prompt. Avoids a dependency on a terminal library for this single check.
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// validateKey checks that the bytes look like a tarsnap key file.
func validateKey(b []byte) error {
	if !bytes.Contains(b, []byte(keyMarker)) {
		return fmt.Errorf("input does not look like a tarsnap key (missing %q marker); "+
			"check that you pasted the full key", keyMarker)
	}
	return nil
}

// renderTarsnaprc builds a tarsnap config file referencing the given absolute
// key and cache paths, with sensible defaults for interactive use.
func renderTarsnaprc(keyFile, cacheDir string) string {
	var b strings.Builder
	b.WriteString("# Generated by 'lazysnap init'. Edit freely.\n")
	fmt.Fprintf(&b, "keyfile %s\n", keyFile)
	fmt.Fprintf(&b, "cachedir %s\n", cacheDir)
	b.WriteString("nodump\n")
	b.WriteString("print-stats\n")
	b.WriteString("humanize-numbers\n")
	b.WriteString("checkpoint-bytes 1G\n")
	return b.String()
}

// writeFile writes data atomically-ish with the given permissions, truncating
// any existing file. (Callers gate overwrites on --force beforehand.)
func writeFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}

const usage = `Usage: lazysnap init [flags]

Bootstrap tarsnap on a new machine from an existing key. The key is read from
stdin (piped or pasted) or from --key-file; lazysnap never contacts a password
manager itself. It writes the key, generates a tarsnaprc, and rebuilds the
local cache with 'tarsnap --fsck'.

Examples:
  pbpaste | lazysnap init                       # paste key from clipboard (macOS)
  lazysnap init --key-file ~/Downloads/tarsnap.key
  lazysnap init                                  # paste, then Ctrl-D

Flags:
  --key-file PATH   read the key from PATH instead of stdin
  --key-out PATH    where to write the key (default ~/.tarsnap.key)
  --cachedir PATH   tarsnap cache directory (default ~/.tarsnap-cache)
  --rc PATH         tarsnaprc to generate (default ~/.tarsnaprc)
  --tarsnap PATH    tarsnap binary (default "tarsnap")
  --force           overwrite an existing key or config
  --no-fsck         skip the 'tarsnap --fsck' cache rebuild
`
