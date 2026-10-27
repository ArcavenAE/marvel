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
// the host's config or a seat's key. Every commit goes through signedCommit,
// which verifies the signature against that key alone. The patterns are
// bracketed so this file cannot match itself.
func TestEveryCommitIsSignedWithTheRunKey(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("view_integration_test.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	bare := regexp.MustCompile(`"[c]ommit"|"[c]ommit-tree"|"[m]ktag"|"[f]ast-import"|"[u]pdate-ref"|-t [c]ommit`)
	inHelper := false
	for i, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(line, "func ") {
			inHelper = strings.HasPrefix(line, "func signedCommit(")
		}
		if !inHelper && bare.MatchString(line) {
			t.Errorf("view_integration_test.go:%d commits outside signedCommit: %s", i+1, strings.TrimSpace(line))
		}
	}
	// signedCommit verifies against the run key, and git reads no host config.
	for _, need := range []string{`func signedCommit\(`, `"verify-[c]ommit"`, `gpg\.ssh\.allowedSignersFile`, `GIT_CONFIG_GLOBAL=/dev/null`} {
		if !regexp.MustCompile(need).MatchString(src) {
			t.Errorf("view_integration_test.go lacks %s", need)
		}
	}
}
