package view

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The separator sits right before the remote, so a remote that starts with a
// dash is an operand and not an option.
func TestFetchArgsPutTheSeparatorRightBeforeTheRemote(t *testing.T) {
	for _, remote := range []string{"origin", "/srv/repo.git", "-dash-remote"} {
		args := fetchArgs("/m", remote, "main")
		i := slices.Index(args, remote)
		if i < 1 || args[i-1] != "--" {
			t.Errorf("fetchArgs remote %q = %q, want -- right before the remote", remote, args)
		}
		if slices.Index(args, "--") != i-1 {
			t.Errorf("fetchArgs remote %q = %q, want one separator", remote, args)
		}
	}
}

// A remote that starts with a dash is looked up as a repository and fails as
// one. It is never parsed as an option.
func TestFetchOfADashRemoteFailsAsAnUnknownRepository(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	err := (ExecGit{}).Fetch(context.Background(), filepath.Join(t.TempDir(), "mirror.git"), "-dash-remote", "main")
	if err == nil {
		t.Fatal("Fetch of a dash remote returned no error")
	}
	msg := err.Error()
	if strings.Contains(msg, "unknown switch") || strings.Contains(msg, "unknown option") || strings.Contains(msg, "usage:") {
		t.Fatalf("the remote was parsed as an option: %v", err)
	}
	if !strings.Contains(msg, "does not appear to be a git repository") && !strings.Contains(msg, "strange pathname") {
		t.Fatalf("Fetch error = %v, want an unknown-repository failure", err)
	}
}
