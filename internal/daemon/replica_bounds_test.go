package daemon

import (
	"encoding/json"
	"net"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// negScaleManifest is one sleep replica, deterministic and needing no model
// auth. The fixture for marvel#365.
const negScaleManifest = `
workspace:
  name: negscale
teams:
  - name: crew
    roles:
      - name: crew
        replicas: 1
        runtime:
          command: sleep
          args: ["300"]
`

// noPanic runs fn and fails the test, rather than the whole test binary, if
// it panics. Before the #365 fix the daemon had no recover anywhere, so the
// panic below took down the process that served every operator.
func noPanic(t *testing.T, what string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s panicked: %v\n%s", what, r, debug.Stack())
		}
	}()
	fn()
}

// TestScaleToNegativeReplicasIsRefused: `marvel scale --replicas -1` is an
// error that names the role, and nothing reaches the store. Before the fix
// handleScale stored -1, reconciled synchronously, and planRole indexed
// current[-1] (marvel#365).
func TestScaleToNegativeReplicasIsRefused(t *testing.T) {
	d := newHandlerDaemon(t)
	if resp := applyManifest(t, d, negScaleManifest); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
	d.teamCtrl.ReconcileOnce()

	var resp Response
	noPanic(t, "scale to -1", func() {
		resp = d.handleScale(mustMarshal(t, scaleParams{TeamKey: "negscale/crew", Role: "crew", Replicas: -1}))
	})
	if resp.Error == "" || !strings.Contains(resp.Error, "crew") {
		t.Errorf("scale to -1: error = %q, want a refusal naming the role", resp.Error)
	}
	team, err := d.store.GetTeam("negscale/crew")
	if err != nil {
		t.Fatalf("get team: %v", err)
	}
	if got := team.Roles[0].Replicas; got != 1 {
		t.Errorf("stored replicas = %d after a refused scale, want 1", got)
	}
}

// TestRestartWithStoredNegativeReplicasDoesNotPanic: a state file that already
// holds a negative count (written by a daemon from before the fix) must not
// crash-loop the next daemon, and must not delete anything. The bad row is
// left in place and reported, not rewritten.
func TestRestartWithStoredNegativeReplicasDoesNotPanic(t *testing.T) {
	skipIfNoTmux(t)
	bolt := t.TempDir() + "/state.bolt"
	d1, err := NewWithOptions(Options{StateBolt: bolt})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if resp := applyManifest(t, d1, negScaleManifest); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
	d1.teamCtrl.ReconcileOnce()
	if err := d1.store.UpdateTeam("negscale/crew", func(live *api.Team) error {
		live.Roles[0].Replicas = -1
		return nil
	}); err != nil {
		t.Fatalf("plant the bad row: %v", err)
	}
	if err := d1.store.CloseBolt(); err != nil {
		t.Fatalf("close bolt: %v", err)
	}

	d2, err := NewWithOptions(Options{StateBolt: bolt})
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	t.Cleanup(func() {
		for _, ws := range d2.store.ListWorkspaces() {
			_ = d2.sessMgr.CleanupWorkspace(ws.Name)
		}
	})
	if got := d2.events.Snapshot(events.Filter{Kind: events.KindRoleReplicasInvalid}, 0); len(got) != 1 {
		t.Errorf("startup reported %d %s events, want 1 naming the bad row", len(got), events.KindRoleReplicasInvalid)
	}
	before := len(d2.store.ListSessionsByTeam("negscale", "crew"))
	noPanic(t, "first reconcile after restart", d2.teamCtrl.ReconcileOnce)
	if after := len(d2.store.ListSessionsByTeam("negscale", "crew")); after != before {
		t.Errorf("sessions %d -> %d across a reconcile of a negative count, want nothing deleted", before, after)
	}
	team, err := d2.store.GetTeam("negscale/crew")
	if err != nil {
		t.Fatalf("get team: %v", err)
	}
	if got := team.Roles[0].Replicas; got != -1 {
		t.Errorf("stored replicas rewritten to %d, want -1 left for the operator to see", got)
	}
}

// TestRecoverRequestTurnsAPanicIntoAnError: a handler that panics answers
// with an error Response instead of killing the daemon, and the daemon goes
// on serving the next request on a new connection.
func TestRecoverRequestTurnsAPanicIntoAnError(t *testing.T) {
	d := newHandlerDaemon(t)
	var resp Response
	noPanic(t, "recoverRequest", func() {
		resp = d.recoverRequest("boom", func() Response { panic("forced") })
	})
	if !strings.Contains(resp.Error, "internal error handling boom") || !strings.Contains(resp.Error, "forced") {
		t.Errorf("error = %q, want an internal-error response naming the method and the panic", resp.Error)
	}

	client, server := net.Pipe()
	go d.handleRWCAs(server, localCaller())
	if err := json.NewEncoder(client).Encode(Request{Method: "get", Params: mustMarshal(t, map[string]string{"resource_type": "workspaces"})}); err != nil {
		t.Fatalf("send: %v", err)
	}
	var next Response
	if err := json.NewDecoder(client).Decode(&next); err != nil {
		t.Fatalf("read: %v", err)
	}
	_ = client.Close()
	if next.Error != "" {
		t.Errorf("next request after a recovered panic: %s", next.Error)
	}
}
