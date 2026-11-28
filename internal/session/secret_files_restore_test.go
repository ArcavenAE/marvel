package session

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/paths"
	"github.com/arcavenae/marvel/internal/tmux"
)

// liveSeat launches a seat so its token and password files exist, records it as
// a running session the way the daemon does, and returns it with both paths.
func liveSeat(t *testing.T, mgr *Manager, plaintextToken bool) (sess *api.Session, tokenPath, passPath string) {
	t.Helper()
	sess = seat("plain", "claude")
	sess.State = api.SessionRunning
	sess.PaneID = "%1"
	plan := mgr.planLaunch(sess)
	tokenPath, passPath = plan.env[api.HeartbeatTokenFileEnv], plan.env[busPassFileEnv]
	if tokenPath == "" || passPath == "" {
		t.Fatalf("launch wrote token file %q and password file %q, want both", tokenPath, passPath)
	}
	if !plaintextToken {
		// An adopted seat after a daemon restart: only the hash is recorded.
		sess.HeartbeatToken = ""
	}
	if err := mgr.store.CreateSession(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return sess, tokenPath, passPath
}

// purge is what the temp-directory sweep does to a seat's files.
func purge(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.Remove(p); err != nil {
			t.Fatalf("purge %s: %v", p, err)
		}
	}
}

func TestTheDefaultTokenDirectoryIsUnderTheMarvelHomeNotTheTempDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.HomeEnv, home)
	t.Setenv("MARVEL_TOKEN_DIR", "")
	want := paths.WithHome(home).SecretsDir()

	got := defaultTokenDir()

	if got != want {
		t.Fatalf("defaultTokenDir() = %q, want the marvel home's secrets directory %q", got, want)
	}
	if got == daemonTempDir("marvel-tokens") {
		t.Errorf("defaultTokenDir() is still the per-layout temp directory %q", got)
	}
}

func TestSecretFilesInTheDefaultDirectoryArePrivateAndUnderTheMarvelHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.HomeEnv, home)
	t.Setenv("MARVEL_TOKEN_DIR", "")
	mgr := busTokenManager(t)
	mgr.TokenDir = defaultTokenDir()
	secrets := paths.WithHome(home).SecretsDir()

	_, tokenPath, passPath := liveSeat(t, mgr, true)

	for _, p := range []string{tokenPath, passPath} {
		if filepath.Dir(p) != secrets {
			t.Errorf("secret file %s is not directly under %s", p, secrets)
		}
		assertPrivateFile(t, p, readFile(t, p))
	}
	di, err := os.Stat(secrets)
	if err != nil {
		t.Fatalf("secrets directory: %v", err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("secrets directory mode = %o, want 700", perm)
	}
}

func TestAPurgedTokenAndPasswordFileAreRestoredOnReconcile(t *testing.T) {
	mgr := busTokenManager(t)
	_, tokenPath, passPath := liveSeat(t, mgr, true)
	purge(t, tokenPath, passPath)

	if n := mgr.RestoreSecretFiles(); n != 2 {
		t.Errorf("RestoreSecretFiles() = %d, want 2 files written", n)
	}

	assertPrivateFile(t, tokenPath, testToken)
	assertPrivateFile(t, passPath, testBusPass)
}

// A daemon restart adopts the seat's pane. The seat keeps the paths it was
// launched with, so a missing file has to come back at adopt time and not wait
// for a reconcile tick.
func TestAPurgedPasswordFileIsRestoredWhenTheSeatIsAdopted(t *testing.T) {
	mgr := busTokenManager(t)
	_, tokenPath, passPath := liveSeat(t, mgr, true)
	purge(t, tokenPath, passPath)
	mgr.driver = adoptDriver{panes: []tmux.PaneInfo{{ID: "%1", PID: "4242"}}}

	adopted, _, err := mgr.reconcilePrefix("marvel-", LeaveUnrecorded)
	if err != nil {
		t.Fatalf("reconcilePrefix: %v", err)
	}
	if adopted != 1 {
		t.Fatalf("adopted = %d, want 1", adopted)
	}

	assertPrivateFile(t, passPath, testBusPass)
	assertPrivateFile(t, tokenPath, testToken)
}

// The store holds only the token's hash, and a restart loses the plaintext.
// The password still comes back; the token file is not invented.
func TestAnAdoptedSeatWithNoPlaintextTokenGetsItsPasswordBackAndNoTokenFile(t *testing.T) {
	mgr := busTokenManager(t)
	_, tokenPath, passPath := liveSeat(t, mgr, false)
	purge(t, tokenPath, passPath)

	if n := mgr.RestoreSecretFiles(); n != 1 {
		t.Errorf("RestoreSecretFiles() = %d, want 1 (the password file)", n)
	}

	assertPrivateFile(t, passPath, testBusPass)
	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Errorf("a token file exists for a seat with no plaintext token (stat err = %v)", err)
	}
}

func TestRestoringSecretFilesNeverLogsASecretValue(t *testing.T) {
	mgr := busTokenManager(t)
	_, tokenPath, passPath := liveSeat(t, mgr, true)
	purge(t, tokenPath, passPath)
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	mgr.RestoreSecretFiles()

	for _, secret := range []string{testToken, testBusPass} {
		if strings.Contains(buf.String(), secret) {
			t.Errorf("the log carries a secret value: %q", buf.String())
		}
	}
}

func TestAFileThatIsStillThereIsLeftAlone(t *testing.T) {
	mgr := busTokenManager(t)
	_, tokenPath, passPath := liveSeat(t, mgr, true)
	if err := os.WriteFile(passPath, []byte("rotated"), 0o600); err != nil {
		t.Fatal(err)
	}

	if n := mgr.RestoreSecretFiles(); n != 0 {
		t.Errorf("RestoreSecretFiles() = %d with nothing missing, want 0", n)
	}

	assertPrivateFile(t, tokenPath, testToken)
	assertPrivateFile(t, passPath, "rotated")
}

func TestAFinishedSessionGetsNoSecretFilesBack(t *testing.T) {
	mgr := busTokenManager(t)
	sess, tokenPath, passPath := liveSeat(t, mgr, true)
	purge(t, tokenPath, passPath)
	if err := mgr.store.UpdateSession(sess.Key(), func(s *api.Session) error {
		s.State = api.SessionSucceeded
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if n := mgr.RestoreSecretFiles(); n != 0 {
		t.Errorf("RestoreSecretFiles() = %d for a finished session, want 0", n)
	}
	for _, p := range []string{tokenPath, passPath} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s was rewritten for a finished session (stat err = %v)", p, err)
		}
	}
}

// adoptDriver is a tmux with one marvel workspace session and the given panes.
// Nothing else is reached by an adopt pass over a recorded pane.
type adoptDriver struct {
	TmuxDriver
	panes []tmux.PaneInfo
}

func (adoptDriver) ListSessions() ([]string, error) { return []string{"marvel-acme"}, nil }

func (d adoptDriver) ListPanes(string) ([]tmux.PaneInfo, error) { return d.panes, nil }
