package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/runtime"
)

// The bus password the admittedBus fixture mints for a team.
const testBusPass = "team-pw"

func busTokenManager(t *testing.T) *Manager {
	t.Helper()
	mgr := tokenManager(t, filepath.Join(t.TempDir(), "tokens"))
	mgr.Bus = admittedBus{}
	return mgr
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

// homeProbeAdapter records the launch context the session manager hands an
// adapter when it asks for the seat's private home, which is where codex
// seeds the env_vars it forwards to the director server.
type homeProbeAdapter struct {
	runtime.Generic
	atHome *runtime.LaunchContext
	seen   runtime.LaunchContext
}

func (*homeProbeAdapter) Name() string { return "homeprobe" }

func (a *homeProbeAdapter) SessionHome(ctx *runtime.LaunchContext) (runtime.SessionHomeSpec, bool) {
	a.atHome, a.seen = ctx, *ctx
	return runtime.SessionHomeSpec{EnvVar: "PROBE_HOME"}, true
}

// The password file is placed before the adapter is asked for the seat's home,
// so the names codex seeds there are the ones the final launch environment
// has: the file variable, and not the value variable passwordByFile removes.
func TestThePasswordFileExistsBeforeTheAdapterSeedsTheSeatHome(t *testing.T) {
	t.Parallel()
	mgr := busTokenManager(t)
	mgr.HarnessHomeDir = t.TempDir()
	mgr.skipBaseCheck = true
	probe := &homeProbeAdapter{}
	mgr.adapters.Register(probe)
	sess := seat("plain", "claude")
	sess.Runtime.Name = "homeprobe"

	plan := mgr.planLaunch(sess)

	if probe.atHome == nil {
		t.Fatal("the adapter was never asked for the seat home")
	}
	if probe.seen.BusPasswordFile == "" {
		t.Fatal("the password file was not placed before the seat home was prepared")
	}
	if probe.seen.BusPasswordFile != plan.env[busPassFileEnv] {
		t.Errorf("file at home time %q, in the launch %q", probe.seen.BusPasswordFile, plan.env[busPassFileEnv])
	}
	assertPrivateFile(t, plan.env[busPassFileEnv], testBusPass)
	if _, present := plan.env[busPassEnv]; present {
		t.Errorf("%s is in the launch environment", busPassEnv)
	}
}
