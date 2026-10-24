package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/team"
)

// handoff.put is a write on a seat's behalf, so it is served only on the
// daemon's own socket (the narrowest grant: the heartbeat token still decides
// whose handoff it is), and never to a credential-push key.
func TestHandoffPutIsLocalOnly(t *testing.T) {
	if !methodRequiresLocal("handoff.put") {
		t.Fatal("handoff.put is not in localOnlyMethods")
	}
	if methodAllowedForScope("handoff.put", ScopeCredentialPush) {
		t.Fatal("a credential-push key may call handoff.put")
	}
	if !methodAllowedForScope("handoff.put", ScopeAdmin) {
		t.Fatal("an admin scope may not call handoff.put")
	}
}

func handoffDaemon(t *testing.T) (*Daemon, string, string, string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := api.NewStore()
	d := &Daemon{store: store, teamCtrl: team.NewController(store, nil)}
	if err := store.CreateWorkspace(&api.Workspace{Name: "ws", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	role := api.Role{
		Name: "worker", Replicas: 1,
		Runtime: api.Runtime{Name: "claude", Command: "claude"},
		Shift: &api.ShiftPolicy{
			Any:     []api.ShiftCondition{{On: api.ShiftTriggerMaxAge, MaxAge: time.Hour}},
			Handoff: filepath.Join(dir, "{session}.md"), HandoffMarker: "END HANDOFF",
		},
	}
	if err := store.CreateTeam(&api.Team{
		Name: "squad", Workspace: "ws", Roles: []api.Role{role}, Generation: 1, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	tok, hash, err := api.NewHeartbeatToken()
	if err != nil {
		t.Fatal(err)
	}
	sess := &api.Session{
		Name: "squad-worker-g1-0", Workspace: "ws", Team: "squad", Role: "worker", Generation: 1,
		Runtime: api.Runtime{Name: "claude", Command: "claude"}, State: api.SessionRunning,
		CreatedAt: time.Now().UTC().Add(-2 * time.Hour), HeartbeatToken: tok, HeartbeatTokenHash: hash,
	}
	if err := store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	var st syscall.Stat_t
	if err := syscall.Stat(dir, &st); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateTeam("ws/squad", func(live *api.Team) error {
		live.ShiftRequests = map[string]api.ShiftRequest{"worker": {
			Session: sess.Key(), Cause: api.ShiftTriggerMaxAge, RequestedAt: time.Now().UTC(),
			DirDev: uint64(st.Dev), DirIno: uint64(st.Ino),
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return d, tok, dir, sess.Name
}

func handoffPutParams(t *testing.T, token, text string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(api.HandoffPutRequest{Token: token, Text: text})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHandleHandoffPutWritesForTheTokenHolder(t *testing.T) {
	d, tok, dir, name := handoffDaemon(t)
	resp := d.dispatchAs(daemonrpc.Request{Method: "handoff.put", Params: handoffPutParams(t, tok, "notes")}, localCaller())
	if resp.Error != "" {
		t.Fatalf("handoff.put: %s", resp.Error)
	}
	var got api.HandoffPutResult
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("decode result %s: %v", resp.Result, err)
	}
	want := filepath.Join(dir, name+".md")
	if got.Path != want {
		t.Fatalf("result path = %q, want %q", got.Path, want)
	}
	if b, _ := os.ReadFile(want); string(b) != "notes\nEND HANDOFF\n" {
		t.Fatalf("file = %q", b)
	}
}

func TestHandleHandoffPutRefusals(t *testing.T) {
	d, _, dir, _ := handoffDaemon(t)
	for _, tc := range []struct {
		name   string
		params json.RawMessage
		want   string
	}{
		{"bad params", json.RawMessage(`{"token":`), "bad params"},
		{"no token", handoffPutParams(t, "", "x"), "token"},
		{"wrong token", handoffPutParams(t, "nope", "x"), "token"},
	} {
		resp := d.dispatchAs(daemonrpc.Request{Method: "handoff.put", Params: tc.params}, localCaller())
		if !strings.Contains(resp.Error, tc.want) {
			t.Errorf("%s: error = %q, want it to contain %q", tc.name, resp.Error, tc.want)
		}
	}
	if des, _ := os.ReadDir(dir); len(des) != 0 {
		t.Fatalf("a refusal wrote %d entries", len(des))
	}
}

// A key that arrives over the mrvl:// tunnel is not the daemon's own socket,
// whatever scope it carries.
func TestHandoffPutRefusedOverTheTunnel(t *testing.T) {
	d, tok, dir, _ := handoffDaemon(t)
	resp := d.dispatchAs(daemonrpc.Request{Method: "handoff.put", Params: handoffPutParams(t, tok, "x")},
		caller{scope: ScopeAdmin, fingerprint: "SHA256:test", local: false})
	if !strings.Contains(resp.Error, "local unix socket") {
		t.Fatalf("error = %q, want a local-only refusal", resp.Error)
	}
	if des, _ := os.ReadDir(dir); len(des) != 0 {
		t.Fatalf("a tunnelled put wrote %d entries", len(des))
	}
}
