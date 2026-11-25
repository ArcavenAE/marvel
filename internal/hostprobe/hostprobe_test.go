package hostprobe

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/arcavenae/marvel/internal/gittest"
)

// GitMainRoot answers about the folder it is given, not about a repository the
// daemon's environment points at: a daemon started from a git hook inherits
// GIT_DIR, and the answer must not follow it.
func TestGitMainRootIgnoresAnInheritedGitDir(t *testing.T) {
	other := gittest.Repo(t, filepath.Join(t.TempDir(), "other"))
	repo := gittest.Repo(t, filepath.Join(t.TempDir(), "repo-a"))
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	root, reason := GitMainRoot(repo)
	if root != repo || reason != "" {
		t.Fatalf("GitMainRoot(%s) = %q, %q, want the folder's own root", repo, root, reason)
	}
}

// plantFsmonitor points a repository's core.fsmonitor at a script that writes
// a marker file inside scratch, and returns the marker's path. The config is
// written to the file directly: gittest refuses -c and config, on purpose.
func plantFsmonitor(t *testing.T, repo string) string {
	t.Helper()
	scratch := t.TempDir()
	marker := filepath.Join(scratch, "marker")
	hook := filepath.Join(scratch, "hook.sh")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(repo, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("[core]\n\tfsmonitor = " + hook + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return marker
}

// gitDirect runs git in the repository the way a caller without gitOut would.
func gitDirect(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	cmd.Env = gittest.Env()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// A repository's own config does not run a program through gitOut. The control
// first shows the planted hook fires when git status runs without the
// hardening, so a quiet marker below means the clearing worked and not that the
// mechanism was missing.
func TestGitOutDoesNotRunAPlantedFsmonitor(t *testing.T) {
	repo := gittest.Repo(t, filepath.Join(t.TempDir(), "seat-repo"))
	if err := os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitDirect(t, repo, "add", "f")
	marker := plantFsmonitor(t, repo)

	gitDirect(t, repo, "status")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("control: git status did not run the planted fsmonitor (%v); the test cannot tell anything", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	top, err := gitOut(repo, "rev-parse", "--show-toplevel")
	if err != nil || top == "" {
		t.Fatalf("gitOut rev-parse = %q, %v", top, err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("gitOut ran the repository's core.fsmonitor")
	}

	// The clearing is a second layer behind the subcommand list. Let status
	// through for this check and see that the clearing alone holds.
	gitOutSubcommands["status"] = true
	t.Cleanup(func() { delete(gitOutSubcommands, "status") })
	if _, err := gitOut(repo, "status", "--porcelain"); err != nil {
		t.Fatalf("gitOut status: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("gitOut ran the repository's core.fsmonitor for status")
	}
}

// gitOut runs only the subcommands it is meant to. status is the one that
// reads the work tree and starts the fsmonitor program.
func TestGitOutRefusesOtherSubcommands(t *testing.T) {
	repo := gittest.Repo(t, filepath.Join(t.TempDir(), "seat-repo"))
	marker := plantFsmonitor(t, repo)
	for _, args := range [][]string{{"status"}, {"log"}, {"config", "--list"}, {}} {
		out, err := gitOut(repo, args...)
		if !errors.Is(err, errGitSubcommand) || out != "" {
			t.Errorf("gitOut(%v) = %q, %v, want errGitSubcommand", args, out, err)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a refused subcommand still ran the repository's core.fsmonitor")
	}
}
