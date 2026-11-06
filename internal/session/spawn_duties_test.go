package session

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

const dutiesManifest = `
[workspace]
name = "test-spawn-duties"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1
  duties = ["gh.read"]

    [team.role.runtime]
    image = "sleep"
    command = "sleep"
    args = ["300"]
`

const dutiesWorkspace = "test-spawn-duties"

func dutiesSession() *api.Session {
	return &api.Session{
		Name: "squad-worker-g1-0", Workspace: dutiesWorkspace, Team: "squad", Role: "worker",
		Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
	}
}

// dutiesFixture is a tmux-backed manager with the role applied, so a spawn
// reads the role's declared duties from the store.
func dutiesFixture(t *testing.T) *Manager {
	t.Helper()
	skipIfNoTmux(t)
	store := api.NewStore()
	mgr, _ := newTmuxManager(t, store)
	m, err := api.ParseManifestBytes([]byte(dutiesManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(dutiesWorkspace) })
	return mgr
}

func dutyStates(mgr *Manager, seat string) map[string]api.DutyState {
	out := map[string]api.DutyState{}
	for _, r := range mgr.store.ListDuties(seat) {
		out[r.Duty] = r.State
	}
	return out
}

func proveDuty(t *testing.T, mgr *Manager, seat, duty string) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := mgr.store.RecordDuty(api.DutyRecord{
		Seat: seat, Duty: duty, State: api.DutyProven, Source: "pane:turn",
		ObservedAt: now, ValidUntil: now.Add(time.Hour), SetBy: api.SetByDaemon,
	}); err != nil {
		t.Fatalf("RecordDuty: %v", err)
	}
}

// A spawn writes the base duties and the role's declared one, each once, as
// unproven, through the daemon's record path.
func TestSpawnWritesDeclaredDutiesUnproven(t *testing.T) {
	mgr := dutiesFixture(t)
	sess := dutiesSession()
	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create: %v", err)
	}
	got := dutyStates(mgr, sess.Key())
	want := map[string]api.DutyState{"first-turn": api.DutyUnproven, "ack": api.DutyUnproven, "gh.read": api.DutyUnproven}
	if len(got) != len(want) {
		t.Fatalf("duty records = %v, want %v", got, want)
	}
	for d, s := range want {
		if got[d] != s {
			t.Errorf("duty %s = %q, want %q", d, got[d], s)
		}
	}
	for _, r := range mgr.store.ListDuties(sess.Key()) {
		if r.SetBy != api.SetByDaemon {
			t.Errorf("duty %s set_by = %q, want %q", r.Duty, r.SetBy, api.SetByDaemon)
		}
	}
}

// A fresh spawn under a reused key resets a proven duty to unproven: the old
// proof was about the process that died.
func TestRespawnResetsAProvenDutyToUnproven(t *testing.T) {
	mgr := dutiesFixture(t)
	sess := dutiesSession()
	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create: %v", err)
	}
	proveDuty(t, mgr, sess.Key(), "first-turn")
	if got := dutyStates(mgr, sess.Key())["first-turn"]; got != api.DutyProven {
		t.Fatalf("setup: first-turn = %q, want proven", got)
	}
	if err := mgr.store.DeleteSession(sess.Key()); err != nil {
		t.Fatalf("delete: %v", err)
	}
	respawn := dutiesSession()
	if err := mgr.Create(respawn); err != nil {
		t.Fatalf("recreate: %v", err)
	}
	if got := dutyStates(mgr, respawn.Key()); got["first-turn"] != api.DutyUnproven || len(got) != 3 {
		t.Errorf("after respawn duties = %v, want all three unproven", got)
	}
}

// Adoption after a daemon restart is not a spawn: it writes nothing, so a duty
// proven before the restart stays proven.
func TestAdoptionLeavesAProvenDutyAlone(t *testing.T) {
	mgr := dutiesFixture(t)
	sess := dutiesSession()
	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create: %v", err)
	}
	proveDuty(t, mgr, sess.Key(), "first-turn")
	if _, _, err := mgr.AdoptOrLeave(); err != nil {
		t.Fatalf("AdoptOrLeave: %v", err)
	}
	if got := dutyStates(mgr, sess.Key())["first-turn"]; got != api.DutyProven {
		t.Errorf("first-turn after adoption = %q, want proven", got)
	}
}
