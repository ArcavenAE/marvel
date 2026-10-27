package api

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"regexp"
	"testing"
	"time"
)

const (
	planSrc   = "host-a:/srv/manifests/main.toml"
	planOther = "host-a:/srv/manifests/ops.toml"
)

// previewManifest names only ws/held, with a role of two replicas.
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
	// A live seat: deferred until step 4.
	mustTeam(t, s, Team{Name: "busy", Workspace: "ws", Claims: claimsOfSources(at, planSrc)})
	mustSession(t, s, "busy", "worker", "busy-worker-g1-0", 1, SessionPending)
	// Named by the file and held, with no live seat: will spawn 2.
	mustTeam(t, s, Team{Name: "held", Workspace: "ws", Claims: claimsOfSources(at, planSrc)})
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
	if want := []string{"ws/gone", "ws/released"}; !reflect.DeepEqual(deleted, want) {
		t.Errorf("would delete = %v, want %v", deleted, want)
	}
	if len(plan.WouldDelete) > 0 {
		gone := plan.WouldDelete[0]
		if want := []PruneSeat{{Session: "ws/gone-worker-g1-0", State: SessionFailed}}; !reflect.DeepEqual(gone.Seats, want) {
			t.Errorf("ws/gone seats = %+v, want %+v", gone.Seats, want)
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
		{Team: "ws/busy", Reason: "live seat, needs step 4"},
		{Team: "ws/night", Reason: "shift in progress; re-run the plan when it ends"},
	}
	if !reflect.DeepEqual(plan.Deferred, want) {
		t.Errorf("deferred = %+v, want %+v", plan.Deferred, want)
	}
	if want := []PruneHeld{{Team: "ws/held", WillSpawn: 2}}; !reflect.DeepEqual(plan.Held, want) {
		t.Errorf("held = %+v, want %+v", plan.Held, want)
	}
}

// The digest is the first 8 hex characters of a sha256 over the source, then
// the sorted team keys and session keys the prune would delete (the step 2
// ruling, Q4).
func TestPlanPruneDigestCoversTheSourceAndTheSet(t *testing.T) {
	s := planStore(t)
	plan := PlanPrune(parsedPreview(t), planSrc, s)
	if !regexp.MustCompile(`^[0-9a-f]{8}$`).MatchString(plan.Digest) {
		t.Fatalf("digest = %q, want 8 lowercase hex characters", plan.Digest)
	}
	input := "source " + planSrc + "\n" +
		"team ws/gone\n" +
		"team ws/released\n" +
		"session ws/gone-worker-g1-0\n"
	sum := sha256.Sum256([]byte(input))
	if want := hex.EncodeToString(sum[:])[:8]; plan.Digest != want {
		t.Errorf("digest = %s, want %s over:\n%s", plan.Digest, want, input)
	}

	if other := PlanPrune(parsedPreview(t), planOther, s); other.Digest == plan.Digest {
		t.Errorf("two sources share digest %s", plan.Digest)
	}
	mustSession(t, s, "gone", "worker", "gone-worker-g1-1", 1, SessionSucceeded)
	if moved := PlanPrune(parsedPreview(t), planSrc, s); moved.Digest == plan.Digest {
		t.Errorf("digest %s did not move when a session joined the set", plan.Digest)
	}
}

// The handoff note on a would-delete team: declared, none or some roles,
// counting only interactive roles, and a headless note when a headless role
// holds a seat (the step 2 ruling, Q8).
func TestPlanPruneNotesHandoffAndHeadlessWaits(t *testing.T) {
	at := time.Now().UTC()
	asks := &ShiftPolicy{Handoff: "/h/{session}.md", HandoffWindow: 2 * time.Minute}
	headless := Runtime{Mode: RuntimeModeHeadless}
	s := NewStore()
	for _, tc := range []struct {
		name  string
		roles []Role
	}{
		{"declared", []Role{{Name: "a", Shift: asks}}},
		{"none", []Role{{Name: "a"}}},
		{"some", []Role{{Name: "a", Shift: asks}, {Name: "b"}}},
		{"batch", []Role{{Name: "a", Runtime: headless}}},
		{"empty", []Role{{Name: "a", Shift: asks}}},
	} {
		mustTeam(t, s, Team{Name: tc.name, Workspace: "ws", Roles: tc.roles, Claims: claimsOfSources(at, planSrc)})
		if tc.name == "empty" {
			continue
		}
		for _, r := range tc.roles {
			mustSession(t, s, tc.name, r.Name, tc.name+"-"+r.Name+"-g1-0", 1, SessionFailed)
		}
	}
	got := map[string][2]string{}
	for _, d := range PlanPrune(parsedPreview(t), planSrc, s).WouldDelete {
		got[d.Team] = [2]string{d.Handoff, d.Headless}
	}
	want := map[string][2]string{
		"ws/declared": {"handoff: declared, window 2m", ""},
		"ws/none":     {"handoff: none", ""},
		"ws/some":     {"handoff: some roles", ""},
		"ws/batch":    {"", "headless: waits up to 5m"},
		"ws/empty":    {"", ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("notes = %v, want %v", got, want)
	}
}
