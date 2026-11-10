package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// noSecretValue fails when any value in env is the heartbeat token, whatever
// the key: the environment is what tmux new-window carries as -e arguments.
func noSecretValue(t *testing.T, env map[string]string) {
	t.Helper()
	for k, v := range env {
		if strings.Contains(v, testToken) {
			t.Errorf("%s carries the token; it would reach the tmux command line", k)
		}
	}
}

func assertPrivateFile(t *testing.T, path, want string) {
	t.Helper()
	if path == "" {
		t.Fatal("no file path in the environment")
	}
	if got := readFile(t, path); got != want {
		t.Errorf("%s holds %q, want %q", path, got, want)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("%s mode = %o, want 600", path, perm)
	}
}

// aae-orc-xs6om: no adapter implements the token-file path, so every harness
// got the token on the tmux command line.
func TestAnAdapterThatNeverAskedForAFileStillGetsTheTokenByFile(t *testing.T) {
	t.Parallel()
	mgr := tokenManager(t, filepath.Join(t.TempDir(), "tokens"))

	plan := mgr.planLaunch(seat("plain", "claude"))

	if _, present := plan.env[api.HeartbeatTokenEnv]; present {
		t.Fatalf("%s is in the environment of a launch that never asked for a file", api.HeartbeatTokenEnv)
	}
	assertPrivateFile(t, plan.env[api.HeartbeatTokenFileEnv], testToken)
	noSecretValue(t, plan.env)
}

func TestTheDirectFallbackAlsoCarriesNoTokenByValue(t *testing.T) {
	t.Parallel()
	mgr := tokenManager(t, filepath.Join(t.TempDir(), "tokens"))
	sess := seat("plain", "claude")
	sess.Team = "no-such-team" // no team: planLaunch takes the direct path

	plan := mgr.planLaunch(sess)

	assertPrivateFile(t, plan.env[api.HeartbeatTokenFileEnv], testToken)
	noSecretValue(t, plan.env)
}
