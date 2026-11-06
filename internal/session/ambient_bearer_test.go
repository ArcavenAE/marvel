package session

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/tmux"
)

const bearerSentinel = "SENTINEL-bearer-value"

// tmuxEnv sets or removes a variable in the test tmux server's global
// environment, and puts it back on cleanup. The package's TestMain points every
// driver at a scratch socket, so this never touches the operator's server.
func tmuxEnv(t *testing.T, name, value string) {
	t.Helper()
	sock := os.Getenv("MARVEL_TMUX_SOCKET")
	if sock == "" {
		t.Skip("no scratch tmux socket")
	}
	set := func(args ...string) {
		if out, err := exec.Command("tmux", append([]string{"-L", sock}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("tmux %v: %v: %s", args, err, out)
		}
	}
	// A server with no session exits at once, and its environment with it, so
	// a keepalive session holds it up while the variable is set.
	_ = exec.Command("tmux", "-L", sock, "new-session", "-d", "-s", "bearer-keepalive").Run()
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", sock, "kill-session", "-t", "=bearer-keepalive").Run() })
	set("set-environment", "-g", name, value)
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", sock, "set-environment", "-g", "-r", name).Run() })
}

func clearBearers(t *testing.T) {
	t.Helper()
	for _, n := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN"} {
		tmuxEnv(t, n, "")
	}
}

func spawnForBearer(t *testing.T, ws string, mutate func(*api.Session), mgrSet func(*Manager)) (*api.Session, *Manager, *api.Store) {
	t.Helper()
	skipIfNoTmux(t)
	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	mgr := NewManager(store, driver)
	if mgrSet != nil {
		mgrSet(mgr)
	}
	if err := store.CreateWorkspace(&api.Workspace{Name: ws}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	sess := &api.Session{
		Name: "t-r-g1-0", Workspace: ws, Team: "t", Role: "r",
		Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
	}
	if mutate != nil {
		mutate(sess)
	}
	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })
	return sess, mgr, store
}

// An ambient session records which bearer variable names the tmux environment
// held at spawn, and neither the record nor the log carries a value (S1, S2).
func TestSpawnRecordsAmbientBearerNotValues(t *testing.T) {
	skipIfNoTmux(t)
	clearBearers(t)
	tmuxEnv(t, "ANTHROPIC_API_KEY", bearerSentinel)
	tmuxEnv(t, "CLAUDE_CODE_OAUTH_TOKEN", bearerSentinel)
	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	sess, _, store := spawnForBearer(t, "test-bearer-record", nil, nil)
	live, err := store.GetSession(sess.Key())
	if err != nil {
		t.Fatal(err)
	}
	if live.AmbientBearer != "api-key+oauth-token" {
		t.Errorf("AmbientBearer = %q, want api-key+oauth-token", live.AmbientBearer)
	}
	data, _ := json.Marshal(live)
	if strings.Contains(string(data), bearerSentinel) || strings.Contains(logged.String(), bearerSentinel) {
		t.Error("a bearer value reached the record or the log")
	}
}

func TestSpawnAmbientWithNoBearerRecordsNone(t *testing.T) {
	skipIfNoTmux(t)
	clearBearers(t)
	sess, _, store := spawnForBearer(t, "test-bearer-none", nil, nil)
	live, _ := store.GetSession(sess.Key())
	if live.AmbientBearer != "none" {
		t.Errorf("AmbientBearer = %q, want none", live.AmbientBearer)
	}
}

// A session that does not run on the operator's own login is not asked.
func TestSpawnNonAmbientRecordsNothing(t *testing.T) {
	skipIfNoTmux(t)
	tmuxEnv(t, "ANTHROPIC_API_KEY", bearerSentinel)
	sess, _, store := spawnForBearer(t, "test-bearer-nonambient", func(s *api.Session) {
		s.Runtime.Env = map[string]string{"AWS_PROFILE": "p"}
	}, nil)
	live, _ := store.GetSession(sess.Key())
	if live.BackendCredentialSource == api.BackendCredentialAmbient {
		t.Fatalf("setup: the session is still ambient")
	}
	if live.AmbientBearer != "" {
		t.Errorf("AmbientBearer = %q, want it never checked", live.AmbientBearer)
	}
}

// When tmux cannot answer, the record says it could not look.
func TestSpawnTmuxFailureRecordsUnknown(t *testing.T) {
	skipIfNoTmux(t)
	sess, _, store := spawnForBearer(t, "test-bearer-unknown", nil, func(m *Manager) {
		m.BearerReader = func(string, string) tmux.BearerPresence { return tmux.BearerUnknown }
	})
	live, _ := store.GetSession(sess.Key())
	if live.AmbientBearer != "unknown" {
		t.Errorf("AmbientBearer = %q, want unknown", live.AmbientBearer)
	}
}

// Adoption keeps what the spawn recorded.
func TestAdoptKeepsAmbientBearer(t *testing.T) {
	skipIfNoTmux(t)
	clearBearers(t)
	tmuxEnv(t, "ANTHROPIC_AUTH_TOKEN", bearerSentinel)
	sess, mgr, store := spawnForBearer(t, "test-bearer-adopt", nil, nil)
	if _, _, err := mgr.AdoptOrKill(); err != nil {
		t.Fatalf("AdoptOrKill: %v", err)
	}
	live, _ := store.GetSession(sess.Key())
	if live.AmbientBearer != "auth-token" {
		t.Errorf("AmbientBearer after adoption = %q, want auth-token", live.AmbientBearer)
	}
}
