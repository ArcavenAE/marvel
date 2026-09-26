package session

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/paths"
)

// codexControlSocket is where codex 0.157's interactive TUI opens its
// app-server control socket under CODEX_HOME. Measured on kinu 2026-09-26:
// under the old per-layout TMPDIR home the path was 155 to 164 bytes, codex
// printed "path must be shorter than SUN_LEN" and exited 1 before any hook
// ran (aae-orc-pt8k).
const testCodexControlSocket = "app-server-control/app-server-control.sock"

// TestDefaultHarnessHomeFitsCodexControlSocket: the default home for a
// session with long names still leaves room for codex's control socket, with
// the base resolved the way codex resolves it (/tmp is /private/tmp on
// macOS).
func TestDefaultHarnessHomeFitsCodexControlSocket(t *testing.T) {
	mgr := &Manager{HarnessHomeDir: defaultHarnessHomeDir()}
	key := "a-rather-long-workspace-name/a-long-team-name-too-g12-3"
	home := mgr.harnessHomeFor(key)
	base := mgr.HarnessHomeDir
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatalf("resolve base: %v", err)
	}
	sock := filepath.Join(resolved, filepath.Base(home), testCodexControlSocket)
	if len(sock) >= paths.MaxUnixSocketPath {
		t.Errorf("codex control socket under the default home is %d bytes, want under %d: %s",
			len(sock), paths.MaxUnixSocketPath, sock)
	}
}

// TestHarnessHomeLeafIsShortStableAndDistinct: the per-session directory is a
// fixed-length name derived from the key, the same on every launch and
// different between keys, so a restart lands back in its own home.
func TestHarnessHomeLeafIsShortStableAndDistinct(t *testing.T) {
	mgr := &Manager{HarnessHomeDir: t.TempDir()}
	a := mgr.harnessHomeFor("ws/team-role-g1-0")
	if a != mgr.harnessHomeFor("ws/team-role-g1-0") {
		t.Error("the home for one key changed between calls")
	}
	if a == mgr.harnessHomeFor("ws/team-role-g1-1") {
		t.Error("two keys share a home")
	}
	long := mgr.harnessHomeFor(strings.Repeat("w", 80) + "/" + strings.Repeat("t", 80))
	if len(filepath.Base(long)) != len(filepath.Base(a)) {
		t.Errorf("leaf length depends on the key: %q vs %q", filepath.Base(long), filepath.Base(a))
	}
}

// TestHarnessHomeBaseRefusesASquattedDir: the short base sits in a shared
// directory, so one that already exists and is not a private directory of
// ours is not used; the fallback is.
func TestHarnessHomeBaseRefusesASquattedDir(t *testing.T) {
	root := t.TempDir()
	fallback := filepath.Join(root, "fallback")

	open := filepath.Join(root, "open")
	if err := os.Mkdir(open, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if got := harnessHomeBase(open, fallback); got != fallback {
		t.Errorf("a group/world-readable base was used: %s", got)
	}

	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := harnessHomeBase(file, fallback); got != fallback {
		t.Errorf("a non-directory base was used: %s", got)
	}

	fresh := filepath.Join(root, "fresh")
	if got := harnessHomeBase(fresh, fallback); got != fresh {
		t.Errorf("a fresh base was not used: %s", got)
	}
	if fi, err := os.Stat(fresh); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("fresh base not created 0700: %v %v", fi, err)
	}
	if got := harnessHomeBase(fresh, fallback); got != fresh {
		t.Errorf("our own existing base was refused: %s", got)
	}
}

// TestCheckHomeSocketReportsAnOverlongPath: a home whose socket would not fit
// is reported by key and length; one that fits says nothing.
func TestCheckHomeSocketReportsAnOverlongPath(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	checkHomeSocket("ws/short", "/tmp/h", testCodexControlSocket)
	if buf.Len() != 0 {
		t.Errorf("a short path was reported: %s", buf.String())
	}
	long := filepath.Join("/tmp", strings.Repeat("d", 90))
	checkHomeSocket("ws/long", long, testCodexControlSocket)
	if !strings.Contains(buf.String(), "ws/long") || !strings.Contains(buf.String(), "unix socket limit") {
		t.Errorf("an overlong path was not reported: %q", buf.String())
	}
}
