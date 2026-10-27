package gittest

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestNoCommitPlumbing: a test may not write an unsigned commit (operator
// ruling, relayed by director 2026-10-27), and these fixtures test where a
// repository resolves, never its history, so they make no commit at all.
// The patterns are bracketed so this file cannot match itself.
func TestNoCommitPlumbing(t *testing.T) {
	t.Parallel()
	checks := []struct {
		path string
		re   *regexp.Regexp
	}{
		{"gittest.go", regexp.MustCompile(`"[c]ommit"|"[c]ommit-tree"|"[m]ktag"|"[f]ast-import"|"[u]pdate-ref"|-t [c]ommit`)},
		{
			"../../.github/workflows/probe-ubuntu-26.yml",
			regexp.MustCompile(`\bgit\b.*\s([c]ommit|[c]ommit-tree|[m]ktag|[f]ast-import|[u]pdate-ref)(\s|$)|-t [c]ommit`),
		},
	}
	for _, c := range checks {
		data, err := os.ReadFile(c.path)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") || strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if c.re.MatchString(line) {
				t.Errorf("%s:%d makes a commit: %s", c.path, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// TestRunRefusesWhatCouldCommit: Run takes only the subcommands the fixtures
// need, none of which makes a commit, and refuses a config override.
func TestRunRefusesWhatCouldCommit(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"commit", "-q", "-m", "x"},
		{"commit-tree", "abc", "-m", "x"},
		{"merge", "topic"},
		{"update-ref", "refs/heads/main", "abc"},
		{"-c", "commit.gpgsign=false", "init"},
		{"init", "--config-env=x=Y"},
		{"-C", "/elsewhere", "init"},
		{},
	} {
		if err := checkArgs(args); err == nil {
			t.Errorf("checkArgs(%q) = nil, want a refusal", args)
		}
	}
	for _, args := range [][]string{{"init", "-q", "--bare"}, {"worktree", "add", "-q", "--orphan", "-b", "x", "/p"}} {
		if err := checkArgs(args); err != nil {
			t.Errorf("checkArgs(%q) = %v, want nil", args, err)
		}
	}
}

// TestNoOtherSpawnAndNoHostConfig: Run is the one place gittest starts a
// process, and its git reads no global or system config.
func TestNoOtherSpawnAndNoHostConfig(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("gittest.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	if n := len(regexp.MustCompile(`\bexec\.[C]ommand|\bos\.[S]tartProcess|\bsyscall\.([E]xec|[F]orkExec)`).FindAllString(src, -1)); n != 1 {
		t.Errorf("gittest.go starts a process in %d places, want 1 (Run)", n)
	}
	for _, need := range []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "checkArgs(args)"} {
		if !strings.Contains(src, need) {
			t.Errorf("gittest.go lacks %s", need)
		}
	}
}
