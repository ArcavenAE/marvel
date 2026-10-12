package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/runtime"
)

const tokenFileManifest = `
[workspace]
name = "acme"

[[team]]
name = "squad"

  [[team.role]]
  name = "filed"
  replicas = 1

    [team.role.runtime]
    image = "filed"
    command = "filed"

  [[team.role]]
  name = "plain"
  replicas = 1

    [team.role.runtime]
    image = "claude"
    command = "claude"
`

// filedAdapter is a generic launch for a harness that may publish its own
// environment, so it asks for the token by file.
type filedAdapter struct{ runtime.Generic }

func (*filedAdapter) Name() string              { return "filed" }
func (*filedAdapter) DeliversTokenByFile() bool { return true }

const testToken = "9c1e77aa5d"

func tokenManager(t *testing.T, tokenDir string) *Manager {
	t.Helper()
	mgr := &Manager{
		store:         api.NewStore(),
		adapters:      runtime.NewRegistry(),
		ProjectionDir: t.TempDir(),
		Events:        events.NewRing(16),
		SocketPath:    "/run/marvel.sock",
		TokenDir:      tokenDir,
	}
	mgr.adapters.Register(&filedAdapter{})
	m, err := api.ParseManifestBytes([]byte(tokenFileManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := m.Apply(mgr.store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return mgr
}

func seat(role, image string) *api.Session {
	s := sessionFor(role, image)
	s.HeartbeatToken = testToken
	return s
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

func TestAByFileLaunchPutsTheTokenInAPrivateFileAndOnlyItsPathInTheEnvironment(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "tokens")
	mgr := tokenManager(t, dir)

	plan := mgr.planLaunch(seat("filed", "filed"))

	if v, present := plan.env[api.HeartbeatTokenEnv]; present {
		t.Fatalf("%s = %q in the environment of a by-file launch", api.HeartbeatTokenEnv, v)
	}
	path := plan.env[api.HeartbeatTokenFileEnv]
	if path == "" {
		t.Fatalf("no %s in %v", api.HeartbeatTokenFileEnv, plan.env)
	}
	if got := readFile(t, path); got != testToken {
		t.Errorf("token file holds %q, want the session's token", got)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("token file mode = %o, want 600", perm)
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("token directory mode = %o, want 700", perm)
	}
}

func TestADirectFallbackForAByFileRuntimeStillDeliversByFile(t *testing.T) {
	t.Parallel()
	mgr := tokenManager(t, filepath.Join(t.TempDir(), "tokens"))
	sess := seat("filed", "filed")
	sess.Team = "no-such-team" // no team: planLaunch takes the direct path

	plan := mgr.planLaunch(sess)

	if v, present := plan.env[api.HeartbeatTokenEnv]; present {
		t.Fatalf("%s = %q on the direct path of a by-file runtime", api.HeartbeatTokenEnv, v)
	}
	if got := readFile(t, plan.env[api.HeartbeatTokenFileEnv]); got != testToken {
		t.Errorf("token file holds %q, want the session's token", got)
	}
}

func TestAByFileLaunchThatCannotWriteItsFileCarriesNoTokenAtAll(t *testing.T) {
	t.Parallel()
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr := tokenManager(t, filepath.Join(blocker, "tokens")) // a directory cannot be made under a file

	plan := mgr.planLaunch(seat("filed", "filed"))

	for _, k := range []string{api.HeartbeatTokenEnv, api.HeartbeatTokenFileEnv} {
		if v, present := plan.env[k]; present {
			t.Errorf("%s = %q after the token file could not be written; the launch must not fall back to the environment", k, v)
		}
	}
}

func TestALaunchWhoseAdapterDoesNotAskForAFileKeepsTheEnvironmentToken(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "tokens")
	mgr := tokenManager(t, dir)

	plan := mgr.planLaunch(seat("plain", "claude"))

	if got := plan.env[api.HeartbeatTokenEnv]; got != testToken {
		t.Errorf("%s = %q, want the token", api.HeartbeatTokenEnv, got)
	}
	if _, present := plan.env[api.HeartbeatTokenFileEnv]; present {
		t.Errorf("%s set for an adapter that did not ask for a file", api.HeartbeatTokenFileEnv)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Errorf("token directory %s created for a launch that wrote no file", dir)
	}
}

func TestDroppingASessionRemovesItsTokenFile(t *testing.T) {
	t.Parallel()
	mgr := tokenManager(t, filepath.Join(t.TempDir(), "tokens"))
	sess := seat("filed", "filed")
	plan := mgr.planLaunch(sess)
	path := plan.env[api.HeartbeatTokenFileEnv]
	if path == "" {
		t.Fatal("no token file to remove")
	}
	sess.State = api.SessionRunning
	if err := mgr.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}

	if err := mgr.dropSession(*sess, "test"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("token file %s survives its session (stat err: %v)", path, err)
	}
}

func TestEachSessionGetsItsOwnTokenFile(t *testing.T) {
	t.Parallel()
	mgr := tokenManager(t, filepath.Join(t.TempDir(), "tokens"))
	a := seat("filed", "filed")
	b := seat("filed", "filed")
	b.Name = "squad-filed-g1-1"
	b.HeartbeatToken = "another-token"

	pa := mgr.planLaunch(a).env[api.HeartbeatTokenFileEnv]
	pb := mgr.planLaunch(b).env[api.HeartbeatTokenFileEnv]

	if pa == "" || pa == pb {
		t.Fatalf("token files %q and %q must be two different files", pa, pb)
	}
	if got := readFile(t, pa); got != testToken {
		t.Errorf("first file holds %q after the second launch", got)
	}
}

func TestADirectLaunchThatCannotWriteItsFileCarriesNoTokenAtAll(t *testing.T) {
	t.Parallel()
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr := tokenManager(t, filepath.Join(blocker, "tokens"))
	sess := seat("filed", "filed")
	sess.Team = "no-such-team" // no team: planLaunch takes the direct path

	plan := mgr.planLaunch(sess)

	for _, k := range []string{api.HeartbeatTokenEnv, api.HeartbeatTokenFileEnv} {
		if v, present := plan.env[k]; present {
			t.Errorf("%s = %q on the direct path after the token file could not be written", k, v)
		}
	}
}

// A shared temp directory lets another account pre-create the token directory
// wide open. A directory that is not private is refused, and the launch
// carries no token, as it does for any other write failure.
func TestATokenDirectoryThatIsNotPrivateIsRefused(t *testing.T) {
	t.Parallel()
	for name, direct := range map[string]bool{"adapter path": false, "direct path": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := filepath.Join(t.TempDir(), "tokens")
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			mgr := tokenManager(t, dir)
			sess := seat("filed", "filed")
			if direct {
				sess.Team = "no-such-team"
			}

			plan := mgr.planLaunch(sess)

			for _, k := range []string{api.HeartbeatTokenEnv, api.HeartbeatTokenFileEnv} {
				if v, present := plan.env[k]; present {
					t.Errorf("%s = %q although the token directory is mode 755", k, v)
				}
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Errorf("%d entries written into a directory that is not private", len(entries))
			}
		})
	}
}

func TestATokenDirectoryThatIsASymlinkIsRefused(t *testing.T) {
	t.Parallel()
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "tokens")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(real, 0o700); err != nil {
		t.Fatal(err)
	}
	mgr := tokenManager(t, link)

	plan := mgr.planLaunch(seat("filed", "filed"))

	if v, present := plan.env[api.HeartbeatTokenFileEnv]; present {
		t.Errorf("%s = %q through a symlinked token directory", api.HeartbeatTokenFileEnv, v)
	}
}
