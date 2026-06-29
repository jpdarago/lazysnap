package setup

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fakeKey = "# START OF TARSNAP KEY FILE\nc29tZSBiYXNlNjQ=\n# END OF TARSNAP KEY FILE\n"

func TestValidateKey(t *testing.T) {
	if err := validateKey([]byte(fakeKey)); err != nil {
		t.Errorf("valid key rejected: %v", err)
	}
	if err := validateKey([]byte("not a key")); err == nil {
		t.Error("expected error for non-key input")
	}
}

func TestRenderTarsnaprc(t *testing.T) {
	rc := renderTarsnaprc("/home/u/.tarsnap.key", "/home/u/.tarsnap-cache")
	for _, want := range []string{
		"keyfile /home/u/.tarsnap.key",
		"cachedir /home/u/.tarsnap-cache",
		"nodump",
	} {
		if !strings.Contains(rc, want) {
			t.Errorf("rendered tarsnaprc missing %q:\n%s", want, rc)
		}
	}
}

// baseOpts returns options writing into a temp dir, with fsck disabled and the
// key supplied via stdin.
func baseOpts(t *testing.T, key string) options {
	t.Helper()
	dir := t.TempDir()
	return options{
		keyOut:   filepath.Join(dir, ".tarsnap.key"),
		cacheDir: filepath.Join(dir, "cache"),
		rcPath:   filepath.Join(dir, ".tarsnaprc"),
		tarsnap:  "tarsnap",
		runFsck:  false,
		stdin:    strings.NewReader(key),
		stderr:   io.Discard,
	}
}

func TestRunWritesKeyAndConfig(t *testing.T) {
	o := baseOpts(t, fakeKey)
	if err := run(o); err != nil {
		t.Fatalf("run: %v", err)
	}

	got, err := os.ReadFile(o.keyOut)
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	if string(got) != fakeKey {
		t.Errorf("key contents = %q, want %q", got, fakeKey)
	}

	info, err := os.Stat(o.keyOut)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("key perms = %o, want 600", perm)
	}

	rc, err := os.ReadFile(o.rcPath)
	if err != nil {
		t.Fatalf("read rc: %v", err)
	}
	if !strings.Contains(string(rc), "keyfile "+o.keyOut) {
		t.Errorf("tarsnaprc does not reference key path:\n%s", rc)
	}

	if fi, err := os.Stat(o.cacheDir); err != nil || !fi.IsDir() {
		t.Errorf("cache dir not created: %v", err)
	}
}

func TestRunRefusesOverwrite(t *testing.T) {
	o := baseOpts(t, fakeKey)
	if err := os.WriteFile(o.keyOut, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(o); err == nil {
		t.Fatal("expected error when key already exists without --force")
	}
	// The existing key must be left untouched.
	got, _ := os.ReadFile(o.keyOut)
	if string(got) != "existing" {
		t.Errorf("existing key was modified: %q", got)
	}

	o.force = true
	o.stdin = strings.NewReader(fakeKey)
	if err := run(o); err != nil {
		t.Fatalf("run with --force: %v", err)
	}
	got, _ = os.ReadFile(o.keyOut)
	if string(got) != fakeKey {
		t.Errorf("force did not overwrite key: %q", got)
	}
}

func TestRunRejectsBadKey(t *testing.T) {
	o := baseOpts(t, "garbage clipboard contents")
	if err := run(o); err == nil {
		t.Fatal("expected error for invalid key")
	}
	if _, err := os.Stat(o.keyOut); !os.IsNotExist(err) {
		t.Error("key file should not be written when validation fails")
	}
}
