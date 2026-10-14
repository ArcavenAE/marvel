package session

import "testing"

// The manager hands the daemon's cluster name to the launch, so a spawned
// seat's BEADS_ACTOR is its slot URI; with no cluster it keeps today's value.
func TestPlanLaunchCarriesTheSeatURIAsBeadsActor(t *testing.T) {
	t.Parallel()
	mgr := sessionIDManager(t)
	mgr.SeatCluster = "c1"
	got := mgr.planLaunch(sessionFor("reviewer", "claude")).env["BEADS_ACTOR"]
	if want := "marvel://c1/acme/squad/reviewer-0"; got != want {
		t.Errorf("BEADS_ACTOR = %q, want %q", got, want)
	}

	mgr.SeatCluster = ""
	got = mgr.planLaunch(sessionFor("reviewer", "claude")).env["BEADS_ACTOR"]
	if want := "marvel/acme/squad-reviewer-g1-0"; got != want {
		t.Errorf("BEADS_ACTOR with no cluster = %q, want today's %q", got, want)
	}
}
