package api

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

const (
	planSrc   = "host-a:/srv/manifests/main.toml"
	planOther = "host-a:/srv/manifests/ops.toml"
)

// previewManifest names ws/held, with a role short of two replicas and a role
// over its one, and ws/steady, which converges.
const previewManifest = `
[workspace]
name = "ws"

[[team]]
name = "held"

  [[team.role]]
  name = "worker"
  replicas = 2

    [team.role.runtime]
    command = "bash"

  [[team.role]]
  name = "extra"
  replicas = 1

    [team.role.runtime]
    command = "bash"

[[team]]
name = "steady"

  [[team.role]]
  name = "worker"
  replicas = 2

    [team.role.runtime]
    command = "bash"
`

func parsedPreview(t *testing.T) *Manifest {
	t.Helper()
	m, err := ParseManifestBytes([]byte(previewManifest))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func claimsOfSources(at time.Time, sources ...string) *TeamClaims {
	c := &TeamClaims{Sources: map[string]time.Time{}}
	for _, s := range sources {
		c.Sources[s] = at
	}
	return c
}

func mustTeam(t *testing.T, s *Store, team Team) {
	t.Helper()
	if len(team.Roles) == 0 {
		team.Roles = []Role{{Name: "worker"}}
	}
	if err := s.CreateTeam(&team); err != nil {
		t.Fatalf("create team %s/%s: %v", team.Workspace, team.Name, err)
	}
}

func mustSession(t *testing.T, s *Store, team, role, name string, gen int64, state SessionState) {
	t.Helper()
	if err := s.CreateSession(&Session{Name: name, Workspace: "ws", Team: team, Role: role, Generation: gen, State: state}); err != nil {
		t.Fatalf("create session %s: %v", name, err)
	}
}

// planStore holds one team for every group of section 4, as the ruling on
// step 2 defines them, plus teams that must not be listed at all.
func planStore(t *testing.T) *Store {
	t.Helper()
	at := time.Date(2026, 10, 27, 18, 2, 0, 0, time.UTC)
	s := NewStore()
	// Claimed only by this source, which no longer names it: would delete.
	mustTeam(t, s, Team{Name: "gone", Workspace: "ws", Claims: claimsOfSources(at, planSrc)})
	mustSession(t, s, "gone", "worker", "gone-worker-g1-0", 1, SessionFailed)
	// Released by this source on an earlier apply: still this file's to prune.
	mustTeam(t, s, Team{Name: "released", Workspace: "ws", Claims: &TeamClaims{Sources: map[string]time.Time{}, ReleasedBy: planSrc, ReleasedAt: at}})
	// Claimed by this source and another: kept.
	mustTeam(t, s, Team{Name: "shared", Workspace: "ws", Claims: claimsOfSources(at, planSrc, planOther)})
	// Unclaimed, in another workspace: kept, delete by hand.
	mustTeam(t, s, Team{Name: "fossil", Workspace: "other"})
	// Holds a marvel run session: kept.
	mustTeam(t, s, Team{Name: "scratch", Workspace: "ws", Claims: claimsOfSources(at, planSrc)})
	mustSession(t, s, "scratch", "worker", "scratch-run", 0, SessionRunning)
	// Mid-shift: deferred.
	mustTeam(t, s, Team{Name: "night", Workspace: "ws", Claims: claimsOfSources(at, planSrc), Shift: ShiftState{Phase: ShiftDraining}})
	// A live interactive seat: listed to delete, and retired with a handoff
	// ask as the team drains (step 4).
	mustTeam(t, s, Team{Name: "busy", Workspace: "ws", Claims: claimsOfSources(at, planSrc)})
	mustSession(t, s, "busy", "worker", "busy-worker-g1-0", 1, SessionPending)
	// A live headless seat: deferred, left alone by operator ruling 2026-10-28.
	mustTeam(t, s, Team{Name: "batch", Workspace: "ws", Claims: claimsOfSources(at, planSrc), Roles: []Role{{Name: "run", Runtime: Runtime{Mode: RuntimeModeHeadless}}}})
	mustSession(t, s, "batch", "run", "batch-run-g1-0", 1, SessionRunning)
	// Named by the file and held. worker has none of its two seats and extra
	// has three for its one, so a re-apply spawns 2: the excess counts as none.
	mustTeam(t, s, Team{Name: "held", Workspace: "ws", Claims: claimsOfSources(at, planSrc)})
	for i := range 3 {
		mustSession(t, s, "held", "extra", fmt.Sprintf("held-extra-g1-%d", i), 1, SessionRunning)
	}
	// Named by the file and converging: not held, so not listed.
	mustTeam(t, s, Team{Name: "steady", Workspace: "ws", Claims: claimsOfSources(at, planSrc), ConvergencePosture: PostureConverge})
	// Not this file's: another source released it, and another claims it alone.
	mustTeam(t, s, Team{Name: "theirs", Workspace: "ws", Claims: &TeamClaims{Sources: map[string]time.Time{}, ReleasedBy: planOther, ReleasedAt: at}})
	mustTeam(t, s, Team{Name: "ops", Workspace: "ws", Claims: claimsOfSources(at, planOther)})
	return s
}

// Section 4 and the step 2 ruling: each team lands in exactly one group, rule 1
// is predicted from the store before the apply, and teams that are not this
// file's are not listed.
func TestPlanPruneSortsEveryTeamIntoItsGroup(t *testing.T) {
	plan := PlanPrune(parsedPreview(t), planSrc, planStore(t))

	var deleted []string
	for _, d := range plan.WouldDelete {
		deleted = append(deleted, d.Team)
	}
	if want := []string{"ws/busy", "ws/gone", "ws/released"}; !reflect.DeepEqual(deleted, want) {
		t.Errorf("would delete = %v, want %v", deleted, want)
	}
	for _, d := range plan.WouldDelete {
		if d.Team != "ws/gone" {
			continue
		}
		if want := []PruneSeat{{Session: "ws/gone-worker-g1-0", State: SessionFailed}}; !reflect.DeepEqual(d.Seats, want) {
			t.Errorf("ws/gone seats = %+v, want %+v", d.Seats, want)
		}
	}
	if len(plan.KeptShared) != 1 || plan.KeptShared[0].Team != "ws/shared" {
		t.Fatalf("kept shared = %+v, want only ws/shared", plan.KeptShared)
	}
	if _, ok := plan.KeptShared[0].Others[planOther]; !ok || len(plan.KeptShared[0].Others) != 1 {
		t.Errorf("ws/shared others = %v, want only %s", plan.KeptShared[0].Others, planOther)
	}
	if want := []string{"other/fossil"}; !reflect.DeepEqual(plan.KeptUnclaimed, want) {
		t.Errorf("kept unclaimed = %v, want %v", plan.KeptUnclaimed, want)
	}
	if want := []PruneRun{{Team: "ws/scratch", Runs: 1}}; !reflect.DeepEqual(plan.KeptRun, want) {
		t.Errorf("kept for run sessions = %+v, want %+v", plan.KeptRun, want)
	}
	want := []PruneDeferred{
		{Team: "ws/batch", Reason: "live headless seat, left alone by operator ruling 2026-10-28"},
		{Team: "ws/night", Reason: "shift in progress; re-run the plan when it ends"},
	}
	if !reflect.DeepEqual(plan.Deferred, want) {
		t.Errorf("deferred = %+v, want %+v", plan.Deferred, want)
	}
	if want := []PruneHeld{{Team: "ws/held", WillSpawn: 2}}; !reflect.DeepEqual(plan.Held, want) {
		t.Errorf("held = %+v, want %+v", plan.Held, want)
	}
}

// The handoff note on a team's alive seats: declared, none or some roles,
// counting only interactive roles, so a team whose seated roles are all
// headless gets no note (the step 2 ruling, Q8). The note is checked on
// pruneDelete itself.
func TestPruneDeleteNotesHandoff(t *testing.T) {
	asks := &ShiftPolicy{Handoff: "/h/{session}.md", HandoffWindow: 2 * time.Minute}
	headless := Runtime{Mode: RuntimeModeHeadless}
	for _, tc := range []struct {
		name    string
		roles   []Role
		seated  bool
		handoff string
	}{
		{"declared", []Role{{Name: "a", Shift: asks}}, true, "handoff: declared, window 2m"},
		{"none", []Role{{Name: "a"}}, true, "handoff: none"},
		{"some", []Role{{Name: "a", Shift: asks}, {Name: "b"}}, true, "handoff: some roles"},
		{"longest", []Role{{Name: "a", Shift: &ShiftPolicy{Handoff: "/h/{session}.md", HandoffWindow: 7 * time.Minute}}, {Name: "b", Shift: asks}}, true, "handoff: declared, window 7m"},
		{"all headless", []Role{{Name: "a", Runtime: headless, Shift: asks}, {Name: "b", Runtime: headless}}, true, ""},
		{"empty", []Role{{Name: "a", Shift: asks}}, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			team := Team{Name: tc.name, Workspace: "ws", Roles: tc.roles}
			var sessions []Session
			if tc.seated {
				for _, r := range tc.roles {
					sessions = append(sessions, Session{Name: tc.name + "-" + r.Name + "-g1-0", Workspace: "ws", Team: tc.name, Role: r.Name, Generation: 1, State: SessionRunning})
				}
			}
			d := pruneDelete(&team, sessions)
			if d.Handoff != tc.handoff {
				t.Errorf("note = %q, want %q", d.Handoff, tc.handoff)
			}
		})
	}
}

// A would-delete team whose only seats have finished gets no handoff note: a
// seat not in an alive state is deleted at once, never asked
// (docs/design/work-prune.md section 6.2).
func TestPlanPruneNotesNothingForFinishedSeats(t *testing.T) {
	at := time.Now().UTC()
	asks := &ShiftPolicy{Handoff: "/h/{session}.md", HandoffWindow: 2 * time.Minute}
	s := NewStore()
	mustTeam(t, s, Team{Name: "done", Workspace: "ws", Roles: []Role{{Name: "a", Shift: asks}}, Claims: claimsOfSources(at, planSrc)})
	mustSession(t, s, "done", "a", "done-a-g1-0", 1, SessionSucceeded)
	mustTeam(t, s, Team{Name: "ran", Workspace: "ws", Roles: []Role{{Name: "a", Runtime: Runtime{Mode: RuntimeModeHeadless}}}, Claims: claimsOfSources(at, planSrc)})
	mustSession(t, s, "ran", "a", "ran-a-g1-0", 1, SessionSucceeded)

	plan := PlanPrune(parsedPreview(t), planSrc, s)
	if len(plan.WouldDelete) != 2 {
		t.Fatalf("would delete = %+v, want ws/done and ws/ran", plan.WouldDelete)
	}
	for _, d := range plan.WouldDelete {
		if d.Handoff != "" {
			t.Errorf("%s note = %q, want none for a team whose seats have finished", d.Team, d.Handoff)
		}
	}
}
