package view

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestEveryCommitIsSignedWithTheRunKey: the real-git tests need commit
// history, so each commit is signed with a throwaway key made for the run
// (operator ruling, option A, relayed by director 2026-10-27), never with
// the host's config or a seat's key. The patterns are bracketed so this file
// cannot match itself.
//
// Each rule names the only functions in view_integration_test.go that may
// contain a match: a commit only in signedCommit, a process spawn only in
// runGit and runSigningKey (so a git reached through a variable is caught
// too), and the unchecked runners only from the helpers that own them.
func TestEveryCommitIsSignedWithTheRunKey(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("view_integration_test.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	rules := []struct {
		what  string
		re    *regexp.Regexp
		owner map[string]bool
	}{
		{"commits", regexp.MustCompile(`"[c]ommit"|"[c]ommit-tree"|"[m]ktag"|"[f]ast-import"|"[u]pdate-ref"|-t [c]ommit`), map[string]bool{"signedCommit": true}},
		{"spawns a process", regexp.MustCompile(`\bexec\.[C]ommand|\bos\.[S]tartProcess|\bsyscall\.([E]xec|[F]orkExec)`), map[string]bool{"runGit": true, "runSigningKey": true}},
		{"runs git unchecked", regexp.MustCompile(`\b[m]ustRunGit\(`), map[string]bool{"mustRunGit": true, "signedCommit": true, "runSigningKey": true}},
		{"runs git directly", regexp.MustCompile(`\b[r]unGit\(`), map[string]bool{"runGit": true, "gitIn": true, "gitTry": true, "mustRunGit": true}},
	}
	funcName := regexp.MustCompile(`^func (\w+)\(`)
	fn := ""
	for i, line := range strings.Split(src, "\n") {
		if m := funcName.FindStringSubmatch(line); m != nil {
			fn = m[1]
		} else if strings.HasPrefix(line, "func ") || strings.HasPrefix(line, "var ") || strings.HasPrefix(line, "type ") {
			fn = ""
		}
		for _, r := range rules {
			if r.re.MatchString(line) && !r.owner[fn] {
				t.Errorf("view_integration_test.go:%d %s outside %v: %s", i+1, r.what, keys(r.owner), strings.TrimSpace(line))
			}
		}
	}
	// signedCommit verifies against the run key, and git reads no host
	// config and no agent.
	for _, need := range []string{`"verify-[c]ommit"`, `gpg\.ssh\.allowedSignersFile`, `GIT_CONFIG_GLOBAL=/dev/null`, `GIT_CONFIG_NOSYSTEM=1`, `SSH_AUTH_SOCK=`} {
		if !regexp.MustCompile(need).MatchString(src) {
			t.Errorf("view_integration_test.go lacks %s", need)
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestGitInRefusesWhatCouldCommit: gitIn runs only subcommands that make no
// commit, and refuses a config override on the command line or a change to
// how commits are signed, in any letter case.
func TestGitInRefusesWhatCouldCommit(t *testing.T) {
	t.Parallel()
	refused := [][]string{
		{"commit", "-q", "-m", "x"},
		{"merge", "topic"},
		{"cherry-pick", "abc"},
		{"tag", "-a", "v1", "-m", "x"},
		{"-c", "commit.gpgsign=false", "add", "-A"},
		{"add", "-c", "commit.gpgSign=no"},
		{"add", "--config-env=commit.gpgsign=V"},
		{"-C", "/elsewhere", "rev-parse", "HEAD"},
		{"config", "commit.gpgSign", "false"},
		{"config", "user.signingkey", "/home/k"},
		{"config", "GPG.format", "openpgp"},
		{"config", "tag.gpgsign", "false"},
		{},
	}
	for _, args := range refused {
		if err := checkGitArgs(args); err == nil {
			t.Errorf("checkGitArgs(%q) = nil, want a refusal", args)
		}
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
		{"rev-parse", "HEAD"},
		{"config", "core.symlinks", "true"},
	} {
		if err := checkGitArgs(args); err != nil {
			t.Errorf("checkGitArgs(%q) = %v, want nil", args, err)
		}
	}
}
