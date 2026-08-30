package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// createColdHeldTeam seeds a workspace + a cold team holding at the start line
// (no sessions, posture=hold), the shape a stale bolt rehydrates into.
func createColdHeldTeam(t *testing.T, d *Daemon, ws, team, role string, replicas int) {
	t.Helper()
	if _, err := d.store.GetWorkspace(ws); err != nil {
		if werr := d.store.CreateWorkspace(&api.Workspace{Name: ws, CreatedAt: time.Now().UTC()}); werr != nil {
			t.Fatalf("create workspace: %v", werr)
		}
	}
	if err := d.store.CreateTeam(&api.Team{
		Name:               team,
		Workspace:          ws,
		Roles:              []api.Role{{Name: role, Replicas: replicas, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}}},
		Generation:         1,
		ConvergencePosture: api.PostureHold,
		CreatedAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create team: %v", err)
	}
}

// TestApplyGivesTeamConvergePosture proves apply is an explicit "make it so":
// the applied team gets the converge posture and spawns, so the aae-orc-cxdf
// start-hold default never turns apply into a no-op.
func TestApplyGivesTeamConvergePosture(t *testing.T) {
	d := newHandlerDaemon(t)
	if resp := applyManifest(t, d, budgetedManifest); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
	tm, err := d.store.GetTeam("fanout/crew")
	if err != nil {
		t.Fatalf("get team: %v", err)
	}
	if tm.Posture() != api.PostureConverge {
		t.Errorf("applied team posture = %q, want converge", tm.Posture())
	}
	if n := api.CountAlive(d.store.ListSessionsByTeam("fanout", "crew")); n != 3 {
		t.Errorf("applied team alive sessions = %d, want 3 (apply converges)", n)
	}
}

// TestConvergeRPCSpawnsHeldTeam is the go-line end-to-end at the control plane:
// a cold held team spawns nothing until the converge RPC flips its posture, at
// which point the reconcile the handler drives brings it to strength. This is
// the aae-orc-cxdf fix plus its lever, exercised through the daemon dispatch a
// future majordomo will also use.
func TestConvergeRPCSpawnsHeldTeam(t *testing.T) {
	d := newHandlerDaemon(t)
	createColdHeldTeam(t, d, "ws", "crew", "worker", 3)

	// Held: a reconcile spawns nothing.
	d.teamCtrl.ReconcileOnce()
	if n := len(d.store.ListSessionsByTeamRole("ws", "crew", "worker")); n != 0 {
		t.Fatalf("held team spawned %d session(s) before converge, want 0", n)
	}

	params, _ := json.Marshal(convergeParams{TeamKey: "ws/crew"})
	resp := d.dispatch(Request{Method: "converge", Params: params})
	if resp.Error != "" {
		t.Fatalf("converge: %s", resp.Error)
	}

	var res convergeResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		t.Fatalf("unmarshal converge result: %v", err)
	}
	if res.Posture != string(api.PostureConverge) {
		t.Errorf("result posture = %q, want converge", res.Posture)
	}
	if len(res.Teams) != 1 || res.Teams[0] != "ws/crew" {
		t.Errorf("result teams = %v, want [ws/crew]", res.Teams)
	}
	if len(res.Roles) != 1 || res.Roles[0].Spawn != 3 {
		t.Errorf("result roles = %+v, want one role spawning 3", res.Roles)
	}

	if tm, _ := d.store.GetTeam("ws/crew"); tm.Posture() != api.PostureConverge {
		t.Errorf("team posture after converge = %q, want converge", tm.Posture())
	}
	if n := api.CountAlive(d.store.ListSessionsByTeamRole("ws", "crew", "worker")); n != 3 {
		t.Errorf("converged team alive sessions = %d, want 3", n)
	}
}

// TestConvergeRPCEmptyKeyTargetsAllTeams proves the daemon-wide go-line: an
// empty team key converges every held team at once.
func TestConvergeRPCEmptyKeyTargetsAllTeams(t *testing.T) {
	d := newHandlerDaemon(t)
	createColdHeldTeam(t, d, "ws", "a", "worker", 1)
	createColdHeldTeam(t, d, "ws", "b", "worker", 2)

	resp := d.dispatch(Request{Method: "converge"})
	if resp.Error != "" {
		t.Fatalf("converge all: %s", resp.Error)
	}
	for _, key := range []string{"ws/a", "ws/b"} {
		if tm, _ := d.store.GetTeam(key); tm.Posture() != api.PostureConverge {
			t.Errorf("%s posture = %q, want converge", key, tm.Posture())
		}
	}
	if n := api.CountAlive(d.store.ListSessionsByTeam("ws", "a")); n != 1 {
		t.Errorf("team a alive = %d, want 1", n)
	}
	if n := api.CountAlive(d.store.ListSessionsByTeam("ws", "b")); n != 2 {
		t.Errorf("team b alive = %d, want 2", n)
	}
}
