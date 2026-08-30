package team

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// createPostureTeam creates a team (in the shared planTestWS workspace) with an
// explicit convergence posture. The shared createTeamFixture bakes in
// PostureConverge (it represents a team meant to run); the posture tests need to
// set hold explicitly. The workspace matches addAliveSessions so the two
// compose.
func createPostureTeam(t *testing.T, store *api.Store, team string, posture api.ConvergencePosture, roles []api.Role) {
	t.Helper()
	if _, err := store.GetWorkspace(planTestWS); err != nil {
		if werr := store.CreateWorkspace(&api.Workspace{Name: planTestWS, CreatedAt: time.Now().UTC()}); werr != nil {
			t.Fatalf("create workspace: %v", werr)
		}
	}
	if err := store.CreateTeam(&api.Team{
		Name:               team,
		Workspace:          planTestWS,
		Roles:              roles,
		Generation:         1,
		ConvergencePosture: posture,
		CreatedAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create team: %v", err)
	}
}

// TestPostureWithholds is the gate decision table: posture × live-presence →
// whether a cold spawn is withheld. This is the load-bearing semantic of
// question-convergence-posture — hold withholds ONLY a team with no live
// presence, so a converged team and a held-but-alive team are both maintained.
func TestPostureWithholds(t *testing.T) {
	t.Parallel()
	const ws, team, role = "ws", "crew", "worker"
	tests := []struct {
		name    string
		posture api.ConvergencePosture
		alive   int
		want    bool
	}{
		{"hold + cold => withheld (the cxdf money-spender)", api.PostureHold, 0, true},
		{"hold + one live replica => maintained", api.PostureHold, 1, false},
		{"hold + full strength => maintained", api.PostureHold, 3, false},
		{"converge + cold => spawns (the go-line)", api.PostureConverge, 0, false},
		{"converge + live => maintained", api.PostureConverge, 2, false},
		{"unset posture + cold => withheld (safe default)", "", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := api.NewStore()
			ctrl := NewController(store, nil)
			createPostureTeam(t, store, team, tc.posture, []api.Role{sleepRole(role, 3)})
			addAliveSessions(t, store, team, role, tc.alive)
			tm, err := store.GetTeam(ws + "/" + team)
			if err != nil {
				t.Fatalf("get team: %v", err)
			}
			ctrl.mu.Lock()
			got := ctrl.postureWithholds(&tm)
			ctrl.mu.Unlock()
			if got != tc.want {
				t.Errorf("postureWithholds = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestReconcileRoleWithholdsColdHeldTeam is the direct re-arm guard: a cold team
// holding at the start line must spawn NOTHING. The session manager is nil, so
// any spawn attempt panics rather than passing silently — if a future change
// drops the gate, this test fails loudly. This is the aae-orc-cxdf fix asserted
// at the reconcile boundary.
func TestReconcileRoleWithholdsColdHeldTeam(t *testing.T) {
	t.Parallel()
	store := api.NewStore()
	ctrl := NewController(store, nil)

	const ws, team, role = "ws", "crew", "worker"
	createPostureTeam(t, store, team, api.PostureHold, []api.Role{sleepRole(role, 3)})

	tm, err := store.GetTeam(ws + "/" + team)
	if err != nil {
		t.Fatalf("get team: %v", err)
	}
	ctrl.mu.Lock()
	ctrl.reconcileRole(&tm, &tm.Roles[0]) // must not reach the nil session manager
	ctrl.mu.Unlock()

	if n := len(store.ListSessionsByTeamRole(ws, team, role)); n != 0 {
		t.Fatalf("cold held team spawned %d session(s), want 0", n)
	}
}

// TestInitConvergencePostureFromLivePresence proves posture is decided by live
// presence at start and OVERRIDES whatever the store rehydrated — the
// money-safety guarantee for aae-orc-cxdf. A team with live sessions converges;
// a cold team holds; a cold team that PERSISTED converge (a torn-down fleet
// whose bolt kept the posture) is forced back to hold so it cannot cold-spawn.
func TestInitConvergencePostureFromLivePresence(t *testing.T) {
	t.Parallel()
	store := api.NewStore()
	ctrl := NewController(store, nil)

	// alive team: has a running session -> converge
	createPostureTeam(t, store, "alive", api.PostureHold, []api.Role{sleepRole("w", 2)})
	addAliveSessions(t, store, "alive", "w", 2)

	// cold team: no sessions -> hold
	createPostureTeam(t, store, "cold", api.PostureHold, []api.Role{sleepRole("w", 2)})

	// stale team: persisted converge but no live sessions -> forced to hold
	createPostureTeam(t, store, "stale", api.PostureConverge, []api.Role{sleepRole("w", 2)})

	if err := ctrl.InitConvergencePosture(); err != nil {
		t.Fatalf("InitConvergencePosture: %v", err)
	}

	cases := map[string]api.ConvergencePosture{
		"ws/alive": api.PostureConverge,
		"ws/cold":  api.PostureHold,
		"ws/stale": api.PostureHold, // the money-safety override
	}
	for key, want := range cases {
		tm, err := store.GetTeam(key)
		if err != nil {
			t.Fatalf("get %s: %v", key, err)
		}
		if got := tm.Posture(); got != want {
			t.Errorf("%s posture = %q, want %q", key, got, want)
		}
	}
}

// TestSetConvergencePostureFlipsAndReports proves the control-plane lever
// persists the posture and reports the plan a converge would enact, without
// spawning (SetConvergencePosture does not reconcile). An empty team key targets
// every team.
func TestSetConvergencePostureFlipsAndReports(t *testing.T) {
	t.Parallel()
	store := api.NewStore()
	ctrl := NewController(store, nil)

	createPostureTeam(t, store, "a", api.PostureHold, []api.Role{sleepRole("w", 3)})
	createPostureTeam(t, store, "b", api.PostureHold, []api.Role{sleepRole("w", 1)})

	// Converge one team by key.
	plans, err := ctrl.SetConvergencePosture("ws/a", api.PostureConverge)
	if err != nil {
		t.Fatalf("SetConvergencePosture(ws/a): %v", err)
	}
	if tm, _ := store.GetTeam("ws/a"); tm.Posture() != api.PostureConverge {
		t.Errorf("ws/a posture = %q, want converge", tm.Posture())
	}
	if len(plans) != 1 || plans[0].Action != RoleSpawn || plans[0].Spawn != 3 {
		t.Errorf("plans = %+v, want one RoleSpawn of 3", plans)
	}
	// It did NOT spawn — SetConvergencePosture only sets the lever.
	if n := len(store.ListSessionsByTeamRole("ws", "a", "w")); n != 0 {
		t.Errorf("SetConvergencePosture spawned %d session(s), want 0", n)
	}
	// ws/b untouched.
	if tm, _ := store.GetTeam("ws/b"); tm.Posture() != api.PostureHold {
		t.Errorf("ws/b posture = %q, want hold (untargeted)", tm.Posture())
	}

	// Empty key targets all teams.
	if _, err := ctrl.SetConvergencePosture("", api.PostureConverge); err != nil {
		t.Fatalf("SetConvergencePosture(all): %v", err)
	}
	if tm, _ := store.GetTeam("ws/b"); tm.Posture() != api.PostureConverge {
		t.Errorf("ws/b posture = %q, want converge after all-teams converge", tm.Posture())
	}

	// An unknown posture is rejected.
	if _, err := ctrl.SetConvergencePosture("ws/a", api.ConvergencePosture("bogus")); err == nil {
		t.Error("SetConvergencePosture accepted a bogus posture, want error")
	}
}

// TestReexecAutoResumeAdoptedTeamDoesNotSpawn is the detach+reexec
// non-regression: a team whose panes survived comes back fully adopted (its
// desired replicas are all present), the start path sets it to converge, and the
// reconcile spawns NOTHING because adoption already satisfies desired. This is
// the property the aae-orc-cxdf fix must never break — reexec still auto-resumes.
func TestReexecAutoResumeAdoptedTeamDoesNotSpawn(t *testing.T) {
	t.Parallel()
	store := api.NewStore()
	ctrl := NewController(store, nil) // steady state touches no session manager

	const ws, team, role = "ws", "crew", "worker"
	createPostureTeam(t, store, team, api.PostureHold, []api.Role{sleepRole(role, 3)})
	addAliveSessions(t, store, team, role, 3) // adoption reclaimed all three panes

	if err := ctrl.InitConvergencePosture(); err != nil {
		t.Fatalf("InitConvergencePosture: %v", err)
	}
	if tm, _ := store.GetTeam(ws + "/" + team); tm.Posture() != api.PostureConverge {
		t.Fatalf("adopted team posture = %q, want converge", tm.Posture())
	}

	tm, _ := store.GetTeam(ws + "/" + team)
	ctrl.mu.Lock()
	ctrl.reconcileRole(&tm, &tm.Roles[0])
	ctrl.mu.Unlock()

	if n := len(store.ListSessionsByTeamRole(ws, team, role)); n != 3 {
		t.Fatalf("adopted team changed session count: got %d, want 3 (steady, no spawn)", n)
	}
}

// TestReconcileRoleHoldMaintainsLiveTeam is the load-bearing exception: a team
// that holds at the start line but still has a live replica is being maintained,
// not cold-started, so hold must NOT suppress topping it back up to strength. A
// team that later loses a replica still restarts it (question-convergence-posture).
func TestReconcileRoleHoldMaintainsLiveTeam(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	const ws, team, role = "ws", "crew", "worker"
	createPostureTeam(t, store, team, api.PostureHold, []api.Role{sleepRole(role, 2)})
	addAliveSessions(t, store, team, role, 1) // one replica is alive

	tm, err := store.GetTeam(ws + "/" + team)
	if err != nil {
		t.Fatalf("get team: %v", err)
	}
	ctrl.mu.Lock()
	ctrl.reconcileRole(&tm, &tm.Roles[0])
	ctrl.mu.Unlock()

	if n := api.CountAlive(store.ListSessionsByTeamRole(ws, team, role)); n != 2 {
		t.Fatalf("held team with a live replica was not topped up: alive=%d, want 2", n)
	}
}

// TestReconcileConvergeSpawnsColdTeam is the go-line end-to-end: a cold team the
// operator converges spawns toward desired. Together with
// TestReconcileRoleWithholdsColdHeldTeam (hold spawns nothing) this pins both
// sides of the lever.
func TestReconcileConvergeSpawnsColdTeam(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	const ws, team, role = "ws", "crew", "worker"
	createPostureTeam(t, store, team, api.PostureHold, []api.Role{sleepRole(role, 3)})

	// Held: nothing spawns.
	ctrl.ReconcileOnce()
	if n := len(store.ListSessionsByTeamRole(ws, team, role)); n != 0 {
		t.Fatalf("held cold team spawned %d session(s), want 0", n)
	}

	// The go-line.
	if _, err := ctrl.SetConvergencePosture(ws+"/"+team, api.PostureConverge); err != nil {
		t.Fatalf("SetConvergencePosture: %v", err)
	}
	ctrl.ReconcileOnce()
	if n := len(store.ListSessionsByTeamRole(ws, team, role)); n != 3 {
		t.Fatalf("converged team spawned %d session(s), want 3", n)
	}
}

// TestRefreshLivenessReapsDeadPaneRecordsBeforePosture is the host-reboot
// money-safety guard: a rehydrated session whose pane did NOT survive still
// reads State=Running, so a naive live-count would call the team alive and give
// it converge. RefreshLiveness reaps it first, so InitConvergencePosture sees
// the cold team it is and holds. Without this ordering a rebooted host would
// cold-spawn the fleet (aae-orc-cxdf) on the next start.
func TestRefreshLivenessReapsDeadPaneRecordsBeforePosture(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	const ws, team, role = "ws", "crew", "worker"
	createPostureTeam(t, store, team, api.PostureConverge, []api.Role{sleepRole(role, 1)})
	// A rehydrated Running record whose pane is gone (a host reboot leaves
	// exactly this): non-empty PaneID that no live tmux pane matches.
	if err := store.CreateSession(&api.Session{
		Name:       team + "-" + role + "-g1-0",
		Workspace:  ws,
		Team:       team,
		Role:       role,
		Generation: 1,
		State:      api.SessionRunning,
		PaneID:     "%999999",
	}); err != nil {
		t.Fatalf("seed dead-pane session: %v", err)
	}

	ctrl.RefreshLiveness()
	if got := api.CountAlive(store.ListSessionsByTeam(ws, team)); got != 0 {
		t.Fatalf("dead-pane record still counts as alive after reap: %d", got)
	}
	if err := ctrl.InitConvergencePosture(); err != nil {
		t.Fatalf("InitConvergencePosture: %v", err)
	}
	if tm, _ := store.GetTeam(ws + "/" + team); tm.Posture() != api.PostureHold {
		t.Fatalf("rebooted team posture = %q, want hold (its pane did not survive)", tm.Posture())
	}
}
