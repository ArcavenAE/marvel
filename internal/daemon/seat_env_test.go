package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// TestSeatUnderDirtyDaemonIsCleanAndHeartbeats is BA1's positive and negative
// case at the daemon (aae-orc#418): a daemon started from a Claude Code
// session, starting its own tmux server, spawns a seat that
//   - holds none of the denylisted names (checked by name, never value),
//   - keeps its CLAUDE_CODE_USE_* backend selector,
//   - reaches running, and
//   - heartbeats with the token its pane was actually given, and once the
//     seat is replaced, the previous session's token is refused and the
//     current one accepted (gate A's heartbeat check).
//
// The daemon also reports what it scrubbed, names only.
func TestSeatUnderDirtyDaemonIsCleanAndHeartbeats(t *testing.T) {
	skipIfNoTmux(t)
	for _, n := range api.InheritedSessionEnv {
		t.Setenv(n, "placeholder")
	}
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
	// A fresh server, so the dirty daemon is the process that starts it.
	useFreshTmuxServer(t, fmt.Sprintf("marvel-test-seatenv-d-%d", os.Getpid()))

	d := newHandlerDaemon(t)
	for _, n := range api.InheritedSessionEnv {
		if _, ok := os.LookupEnv(n); ok {
			t.Errorf("daemon still carries %s after start", n)
		}
	}
	ev := d.events.Snapshot(events.Filter{Kind: events.KindDaemonEnvScrubbed}, 0)
	if len(ev) != 1 {
		t.Fatalf("daemon.env-scrubbed events = %d, want 1", len(ev))
	}
	for _, n := range api.InheritedSessionEnv {
		if !strings.Contains(ev[0].Message, n) {
			t.Errorf("scrub event does not name %s", n)
		}
	}
	if strings.Contains(ev[0].Message, "placeholder") {
		t.Error("scrub event carries a value; it must carry names only")
	}

	// A heartbeat token is minted only for a session with a socket to
	// report to; the handler daemon has no listener, so name one.
	d.sessMgr.SocketPath = filepath.Join(t.TempDir(), "m.sock")
	out := filepath.Join(t.TempDir(), "env.txt")
	manifest := `
workspace:
  name: ws418
teams:
  - name: seatenv
    roles:
      - name: worker
        replicas: 1
        runtime:
          command: sh
          args: ["-c", "env > ` + out + `.tmp && mv ` + out + `.tmp ` + out + ` && sleep 300"]
`
	if resp := applyManifest(t, d, manifest); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}

	env := readSeatEnv(t, out)
	for _, n := range api.InheritedSessionEnv {
		if _, ok := env[n]; ok {
			t.Errorf("seat inherited %s", n)
		}
	}
	if _, ok := env["CLAUDE_CODE_USE_BEDROCK"]; !ok {
		t.Error("seat lost CLAUDE_CODE_USE_BEDROCK; backend selectors must pass through")
	}

	sessions := d.store.ListSessionsByTeam("ws418", "seatenv")
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	sess := sessions[0]
	if sess.State != api.SessionRunning {
		t.Errorf("seat state = %s, want running", sess.State)
	}
	tok, ok := env[api.HeartbeatTokenEnv]
	if !ok {
		t.Fatalf("seat has no %s; it could not heartbeat", api.HeartbeatTokenEnv)
	}
	raw, err := json.Marshal(api.NewHeartbeatRequestWithToken(sess.Key(), tok, 12, ""))
	if err != nil {
		t.Fatalf("marshal heartbeat: %v", err)
	}
	if resp := d.handleHeartbeat(raw); resp.Error != "" {
		t.Fatalf("heartbeat with the pane's own token refused: %s", resp.Error)
	}
	after, err := d.store.GetSession(sess.Key())
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if after.LastHeartbeat.IsZero() {
		t.Error("admitted heartbeat did not stamp LastHeartbeat")
	}

	// Gate A's heartbeat check, as the operator defined it: "the previous
	// session's token is refused, the current one accepted." Replace the seat,
	// then present the previous session's token for the current session.
	if err := os.Remove(out); err != nil {
		t.Fatalf("clear env file: %v", err)
	}
	if err := d.sessMgr.Delete(sess.Key()); err != nil {
		t.Fatalf("retire seat: %v", err)
	}
	d.teamCtrl.ReconcileOnce()
	env2 := readSeatEnv(t, out)
	sessions = d.store.ListSessionsByTeam("ws418", "seatenv")
	if len(sessions) != 1 {
		t.Fatalf("sessions after replacement = %d, want 1", len(sessions))
	}
	cur := sessions[0]
	curTok, ok := env2[api.HeartbeatTokenEnv]
	if !ok {
		t.Fatalf("replacement seat has no %s", api.HeartbeatTokenEnv)
	}
	if curTok == tok {
		t.Fatal("replacement seat was given the previous session's token")
	}
	for _, n := range api.InheritedSessionEnv {
		if _, ok := env2[n]; ok {
			t.Errorf("replacement seat inherited %s", n)
		}
	}
	stale, err := json.Marshal(api.NewHeartbeatRequestWithToken(cur.Key(), tok, 12, ""))
	if err != nil {
		t.Fatalf("marshal stale heartbeat: %v", err)
	}
	if resp := d.handleHeartbeat(stale); resp.Error == "" {
		t.Error("heartbeat with the previous session's token was accepted; want refused")
	}
	fresh, err := json.Marshal(api.NewHeartbeatRequestWithToken(cur.Key(), curTok, 12, ""))
	if err != nil {
		t.Fatalf("marshal current heartbeat: %v", err)
	}
	if resp := d.handleHeartbeat(fresh); resp.Error != "" {
		t.Errorf("heartbeat with the current session's token refused: %s", resp.Error)
	}
}

// readSeatEnv waits for a seat to write `env` to out and returns it as a
// map. Assertions read names; only the heartbeat token's value is used, to
// drive the handler.
func readSeatEnv(t *testing.T, out string) map[string]string {
	t.Helper()
	var data []byte
	deadline := time.Now().Add(10 * time.Second)
	for {
		b, err := os.ReadFile(out)
		if err == nil {
			data = b
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("seat never wrote its environment: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			env[k] = v
		}
	}
	return env
}
