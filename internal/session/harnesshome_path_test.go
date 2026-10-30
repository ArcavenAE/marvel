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

// TestDurableHarnessHomeBaseLivesUnderTheMarvelHome: the preferred base is
// <marvel home>/h, private to this user, so a temp cleanup never reaches a
// running seat's login (marvel#1142).
func TestDurableHarnessHomeBaseLivesUnderTheMarvelHome(t *testing.T) {
	layout := paths.WithHome(filepath.Join(t.TempDir(), ".marvel"))
	got, ok := durableHarnessHomeBase(layout, 1<<20)
	if !ok {
		t.Fatal("the durable base was refused with room to spare")
	}
	if want := filepath.Join(layout.Home, "h"); got != want {
		t.Errorf("base = %s, want %s", got, want)
	}
	fi, err := os.Lstat(got)
	if err != nil {
		t.Fatalf("stat base: %v", err)
	}
	if !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Errorf("base is %v, want a 0700 directory", fi.Mode())
	}
}

// TestDurableHarnessHomeBaseRefusesWhenTheSocketCannotFit: a marvel home so
// long that codex's control socket would pass the unix socket limit is not
// used, because codex exits at start when the path is too long (aae-orc-pt8k).
// The edge is measured on the resolved path, so a TMPDIR that is a symlink
// (/var to /private/var on macOS) does not move it.
func TestDurableHarnessHomeBaseRefusesWhenTheSocketCannotFit(t *testing.T) {
	layout := paths.WithHome(filepath.Join(t.TempDir(), ".marvel"))
	if _, ok := durableHarnessHomeBase(layout, 1<<20); !ok {
		t.Fatal("the durable base was refused with room to spare")
	}
	resolved, err := filepath.EvalSymlinks(layout.HarnessHomesDir())
	if err != nil {
		t.Fatalf("resolve base: %v", err)
	}
	edge := max(len(resolved), len(layout.HarnessHomesDir())) + harnessHomeSocketReserve
	if got, ok := durableHarnessHomeBase(layout, edge); ok {
		t.Errorf("a base with no byte to spare was used: %s", got)
	}
	if _, ok := durableHarnessHomeBase(layout, edge+1); !ok {
		t.Error("a base one byte under the limit was refused")
	}
}

// TestDurableHarnessHomeBaseMeasuresTheHandedPathToo: a marvel home reached
// through a long symlink is short once resolved but long as handed to the
// harness, which may not canonicalize it. Both lengths must fit.
func TestDurableHarnessHomeBaseMeasuresTheHandedPathToo(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "r")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(root, strings.Repeat("l", 40))
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	layout := paths.WithHome(link)
	resolvedHome, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// Room for the resolved path but not for the longer handed one.
	limit := len(resolvedHome) + len("/h") + harnessHomeSocketReserve + 1
	if got, ok := durableHarnessHomeBase(layout, limit); ok {
		t.Errorf("a base long as handed to the harness was used: %s", got)
	}
}

// TestDurableHarnessHomeBaseRefusesAnOpenDir: a <home>/h that exists and is
// not a private directory of this user is not used.
func TestDurableHarnessHomeBaseRefusesAnOpenDir(t *testing.T) {
	layout := paths.WithHome(filepath.Join(t.TempDir(), ".marvel"))
	if err := layout.EnsureHome(); err != nil {
		t.Fatalf("ensure home: %v", err)
	}
	if err := os.Mkdir(layout.HarnessHomesDir(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if got, ok := durableHarnessHomeBase(layout, 1<<20); ok {
		t.Errorf("a group/world-readable base was used: %s", got)
	}
}
