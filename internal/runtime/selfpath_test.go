package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeInstall lays out a Homebrew-shaped install under a scratch directory:
// a versioned keg binary, and the prefix's bin/marvel link that points at it.
// It returns the keg binary and the link.
func fakeInstall(t *testing.T) (keg, link string) {
	t.Helper()
	root := t.TempDir()
	keg = filepath.Join(root, "Cellar", "marvel", "0.1.0-alpha.1", "bin", "marvel")
	if err := os.MkdirAll(filepath.Dir(keg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keg, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	link = filepath.Join(root, "bin", "marvel")
	if err := os.Symlink(keg, link); err != nil {
		t.Fatal(err)
	}
	return keg, link
}

// runningAs makes MarvelHookPath see exe as the running binary and path as the
// PATH. No t.Parallel in the callers: it sets PATH for the process.
func runningAs(t *testing.T, exe, path string) {
	t.Helper()
	old := selfExecutable
	selfExecutable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { selfExecutable = old })
	t.Setenv("PATH", path)
}

// A hook seeded into a seat's files must outlive an upgrade: the keg path is
// removed by a cleanup, the prefix link is retargeted. So when the PATH's
// marvel is the running binary, the seeded command names the link.
func TestCodexHookCommandNamesTheStableLink(t *testing.T) {
	keg, link := fakeInstall(t)
	runningAs(t, keg, filepath.Dir(link))
	got, err := codexHookCommand()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "/Cellar/") {
		t.Errorf("hook command %q names a versioned keg path that a cleanup removes", got)
	}
	if want := buildCommand(shellQuote(link), []string{"codex-ctx"}); got != want {
		t.Errorf("hook command = %q, want %q", got, want)
	}
}

// When no link on the PATH reaches this binary, the running binary's own path
// is the only honest answer.
func TestMarvelHookPathFallsBackToTheRunningBinary(t *testing.T) {
	keg, _ := fakeInstall(t)
	other := t.TempDir()
	otherMarvel := filepath.Join(other, "marvel")
	if err := os.WriteFile(otherMarvel, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	dangling := t.TempDir()
	if err := os.Symlink(filepath.Join(dangling, "gone"), filepath.Join(dangling, "marvel")); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"no marvel on the PATH":            t.TempDir(),
		"a different marvel on the PATH":   other,
		"a dangling link on the PATH":      dangling,
		"an empty PATH":                    "",
		"a PATH entry that does not exist": "no-such-dir",
	} {
		runningAs(t, keg, path)
		got, err := MarvelHookPath()
		if err != nil || got != keg {
			t.Errorf("%s: MarvelHookPath = %q, %v, want the running binary %q", name, got, err, keg)
		}
	}
}

// The link wins only when it is the same file, so a stale marvel earlier on
// the PATH does not capture a seat that runs another build.
func TestMarvelHookPathPrefersTheFirstPathMarvelOnlyWhenItIsThisBinary(t *testing.T) {
	keg, link := fakeInstall(t)
	stale := t.TempDir()
	if err := os.WriteFile(filepath.Join(stale, "marvel"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runningAs(t, keg, stale+string(os.PathListSeparator)+filepath.Dir(link))
	got, err := MarvelHookPath()
	if err != nil || got != keg {
		t.Errorf("MarvelHookPath = %q, %v, want the running binary %q when the first PATH marvel is another file", got, err, keg)
	}
}
