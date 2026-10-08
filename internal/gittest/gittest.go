// Package gittest builds throwaway git repositories for tests. Its git calls
// drop the repository variables a git hook sets (GIT_DIR and friends), so a
// test run from the pre-push hook builds its fixtures where it says, not inside
// the repository being pushed.
package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Env is the process environment without the variables that point git at a
// particular repository, plus an identity for the plumbing commit.
func Env() []string {
	var env []string
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "GIT_DIR="), strings.HasPrefix(kv, "GIT_WORK_TREE="),
			strings.HasPrefix(kv, "GIT_COMMON_DIR="), strings.HasPrefix(kv, "GIT_INDEX_FILE="),
			strings.HasPrefix(kv, "GIT_PREFIX="):
		default:
			env = append(env, kv)
		}
	}
	return append(env,
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
}

// Run runs git in dir and returns its trimmed output, failing the test on error.
func Run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// Repo makes a repository at dir with one commit, built with plumbing so the
// fixture needs no signing and no git config, and returns its top-level with
// symlinks resolved.
func Repo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	Run(t, dir, "init", "-q", "-b", "main")
	Run(t, dir, "update-ref", "refs/heads/main", Run(t, dir, "commit-tree", Run(t, dir, "mktree"), "-m", "fixture"))
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

// Worktree adds a linked worktree of repo on a new branch and returns its path
// with symlinks resolved.
func Worktree(t *testing.T, repo, name string) string {
	t.Helper()
	wt := filepath.Join(t.TempDir(), name)
	Run(t, repo, "worktree", "add", "-q", wt, "-b", name)
	real, err := filepath.EvalSymlinks(wt)
	if err != nil {
		t.Fatal(err)
	}
	return real
}
