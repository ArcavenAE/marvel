// Package gittest builds throwaway git repositories for tests. Its git calls
// drop the repository variables a git hook sets (GIT_DIR and friends), so a
// test run from the pre-push hook builds its fixtures where it says, not inside
// the repository being pushed.
package gittest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Env is the process environment without the variables that point git at a
// particular repository, with no global or system config, plus an identity.
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
	return append(env, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
}

// subcommands are the git subcommands Run takes. None of them makes a commit.
var subcommands = map[string]bool{"init": true, "worktree": true}

// checkArgs refuses an argument list that could make a commit or override
// config on the command line.
func checkArgs(args []string) error {
	if len(args) == 0 || !subcommands[args[0]] {
		return fmt.Errorf("git %q: not a subcommand gittest runs", args)
	}
	for _, a := range args {
		if a == "-c" || a == "-C" || strings.HasPrefix(a, "--config-env") {
			return fmt.Errorf("git %q: %s is refused here", args, a)
		}
	}
	return nil
}

// Run runs git in dir and returns its trimmed output, failing the test on
// error. It runs only the subcommands checkArgs allows.
func Run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	if err := checkArgs(args); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// Repo makes a repository at dir and returns its top-level with symlinks
// resolved. It makes no commit: the callers test where a repository and its
// worktrees resolve, never its history, and a test may not write an unsigned
// commit.
func Repo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	Run(t, dir, "init", "-q", "-b", "main")
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

// Worktree adds a linked worktree of repo on a new unborn branch, so it needs
// no commit, and returns its path with symlinks resolved.
func Worktree(t *testing.T, repo, name string) string {
	t.Helper()
	wt := filepath.Join(t.TempDir(), name)
	Run(t, repo, "worktree", "add", "-q", "--orphan", "-b", name, wt)
	real, err := filepath.EvalSymlinks(wt)
	if err != nil {
		t.Fatal(err)
	}
	return real
}
