package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/bus"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/events"
)

// attachTestBus gives the daemon a managed bus manager rooted in a temp dir,
// standing in for what attachBus does at Start from the client config.
func attachTestBus(t *testing.T, d *Daemon, hub string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "nats")
	rb := config.ResolvedBus{Managed: true, Listen: "127.0.0.1:4222", StoreDir: dir, HubURL: hub}
	m, err := bus.NewManager(dir, "kinu", rb, d.store, func() bool {
		_, gerr := d.store.GetCredential(busLeafCredential)
		return gerr == nil
	})
	if err != nil {
		t.Fatal(err)
	}
	d.bus = m
	return dir
}

func readAuth(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, bus.AuthName))
	if err != nil {
		t.Fatalf("read auth: %v", err)
	}
	return string(b)
}

func TestBusRegeneratesOnTeamApplyAndDelete(t *testing.T) {
	d := newHandlerDaemon(t)
	dir := attachTestBus(t, d, "")
	d.regenerateBus("start")
	if auth := readAuth(t, dir); strings.Contains(auth, "user: ops") {
		t.Fatalf("ops present before any team applied:\n%s", auth)
	}

	// A team lands in the store (as apply does) and regenerate follows.
	if err := d.store.CreateWorkspace(&api.Workspace{Name: "acme"}); err != nil {
		t.Fatal(err)
	}
	if err := d.store.CreateTeam(&api.Team{Name: "ops", Workspace: "acme", Roles: []api.Role{{Name: "worker"}}}); err != nil {
		t.Fatal(err)
	}
	d.regenerateBus("apply")
	auth := readAuth(t, dir)
	if !strings.Contains(auth, "user: ops") || !strings.Contains(auth, `"agent.acme.ops.>"`) {
		t.Fatalf("applied team has no confined broker user:\n%s", auth)
	}
	pw, ok := d.bus.TeamPassword("ops")
	if !ok || !strings.Contains(auth, pw) {
		t.Error("minted team password not in the auth file")
	}
	evs := d.events.Snapshot(events.Filter{Kind: events.KindBusRendered}, 0)
	if len(evs) < 2 {
		t.Errorf("bus.rendered events = %d, want one for start and one for apply", len(evs))
	}
	for _, ev := range evs {
		if strings.Contains(ev.Message, pw) {
			t.Error("bus.rendered event leaked a team password")
		}
	}

	// Deleting the team through the RPC path removes its user (revocation).
	params, _ := json.Marshal(map[string]string{"resource_type": "team", "name": "acme/ops"})
	if resp := d.dispatch(Request{Method: "delete", Params: params}); resp.Error != "" {
		t.Fatalf("delete team: %s", resp.Error)
	}
	if auth := readAuth(t, dir); strings.Contains(auth, "user: ops") {
		t.Errorf("deleted team still has a broker user:\n%s", auth)
	}
	if _, ok := d.bus.TeamPassword("ops"); ok {
		t.Error("deleted team's password still held")
	}
}

func TestBusLeafBlockFollowsTheSeedCredential(t *testing.T) {
	d := newHandlerDaemon(t)
	dir := attachTestBus(t, d, "nats-leaf://192.168.100.110:7442")
	d.regenerateBus("start")
	conf, _ := os.ReadFile(filepath.Join(dir, bus.ConfName))
	if strings.Contains(string(conf), "leafnodes") {
		t.Fatal("leaf block rendered with a hub but no seed; must start local-only")
	}

	// Pushing bus/leaf re-renders with the remote; deleting it drops it.
	if resp := d.dispatchAs(Request{Method: "credential.put", Params: putParams(t, busLeafCredential, "SUSEEDFORTEST")}, localCaller()); resp.Error != "" {
		t.Fatalf("put: %s", resp.Error)
	}
	conf, _ = os.ReadFile(filepath.Join(dir, bus.ConfName))
	if !strings.Contains(string(conf), "nkey: $DIRECTOR_LEAF_NKEY") {
		t.Errorf("leaf block missing after the seed was pushed:\n%s", conf)
	}
	if strings.Contains(string(conf), "SUSEEDFORTEST") {
		t.Error("seed value rendered into the conf; it must stay environment-only")
	}
	if resp := d.dispatchAs(Request{Method: "credential.delete", Params: nameParams(t, busLeafCredential)}, localCaller()); resp.Error != "" {
		t.Fatalf("delete: %s", resp.Error)
	}
	conf, _ = os.ReadFile(filepath.Join(dir, bus.ConfName))
	if strings.Contains(string(conf), "leafnodes") {
		t.Error("leaf block still rendered after the seed was deleted")
	}
}
