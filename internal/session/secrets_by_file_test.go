package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

const (
	busPassEnv     = "DIRECTOR_NATS_PASS"
	busPassFileEnv = "DIRECTOR_NATS_PASS_FILE"
)

// The bus password the admittedBus fixture mints for a team.
const testBusPass = "team-pw"

func busTokenManager(t *testing.T) *Manager {
	t.Helper()
	mgr := tokenManager(t, filepath.Join(t.TempDir(), "tokens"))
	mgr.Bus = admittedBus{}
	return mgr
}

// noSecretValue fails when any value in env is one of the secrets, whatever
// the key: the environment is what tmux new-window carries as -e arguments.
func noSecretValue(t *testing.T, env map[string]string) {
	t.Helper()
	for k, v := range env {
		for _, secret := range []string{testToken, testBusPass} {
			if strings.Contains(v, secret) {
				t.Errorf("%s carries a secret value; it would reach the tmux command line", k)
			}
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
	mgr := busTokenManager(t)

	plan := mgr.planLaunch(seat("plain", "claude"))

	if _, present := plan.env[api.HeartbeatTokenEnv]; present {
		t.Fatalf("%s is in the environment of a launch that never asked for a file", api.HeartbeatTokenEnv)
	}
	assertPrivateFile(t, plan.env[api.HeartbeatTokenFileEnv], testToken)
	noSecretValue(t, plan.env)
}

func TestTheBusPasswordReachesTheSeatByFileNotByValue(t *testing.T) {
	t.Parallel()
	for _, image := range []string{"claude", "filed"} {
		mgr := busTokenManager(t)
		role := "plain"
		if image == "filed" {
			role = "filed"
		}

		plan := mgr.planLaunch(seat(role, image))

		if _, present := plan.env[busPassEnv]; present {
			t.Errorf("%s: %s is in the environment", image, busPassEnv)
		}
		assertPrivateFile(t, plan.env[busPassFileEnv], testBusPass)
		if plan.env["DIRECTOR_NATS_USER"] == "" {
			t.Errorf("%s: the bus user was dropped along with the password", image)
		}
		noSecretValue(t, plan.env)
	}
}

func TestTheDirectFallbackAlsoCarriesNeitherSecretByValue(t *testing.T) {
	t.Parallel()
	mgr := busTokenManager(t)
	sess := seat("plain", "claude")
	sess.Team = "no-such-team" // no team: planLaunch takes the direct path

	plan := mgr.planLaunch(sess)

	assertPrivateFile(t, plan.env[api.HeartbeatTokenFileEnv], testToken)
	noSecretValue(t, plan.env)
}

func TestARemovedSessionTakesBothSecretFilesWithIt(t *testing.T) {
	t.Parallel()
	mgr := busTokenManager(t)
	sess := seat("plain", "claude")
	plan := mgr.planLaunch(sess)
	tokenPath, passPath := plan.env[api.HeartbeatTokenFileEnv], plan.env[busPassFileEnv]
	if tokenPath == "" || passPath == "" {
		t.Fatalf("launch wrote token file %q and password file %q, want both", tokenPath, passPath)
	}
	if filepath.Dir(passPath) != filepath.Dir(tokenPath) {
		t.Errorf("password file %s is not under the token directory %s", passPath, filepath.Dir(tokenPath))
	}

	mgr.removeTokenFile(sess.Key())

	for _, p := range []string{tokenPath, passPath} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s survives the session (stat err = %v)", p, err)
		}
	}
}

func TestAPasswordFileThatCannotBeWrittenLeavesNoPasswordAtAll(t *testing.T) {
	t.Parallel()
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr := tokenManager(t, filepath.Join(blocker, "tokens")) // a directory cannot be made under a file
	mgr.Bus = admittedBus{}

	plan := mgr.planLaunch(seat("plain", "claude"))

	if _, present := plan.env[busPassEnv]; present {
		t.Errorf("%s fell back to the environment when its file could not be written", busPassEnv)
	}
	noSecretValue(t, plan.env)
}
