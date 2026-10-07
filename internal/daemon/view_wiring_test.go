package daemon

import "testing"

// The daemon connects the view keeper to the team controller: a view that moved
// is reported to it (OnMoved), the keeper asks it how many superseded trees a
// seat holds (Held), and it asks the keeper to seal them (SealViewTrees). With
// any of the three nil the notice, the hold or the seal silently does nothing,
// and no other test builds the daemon through New with a resolvable state
// directory.
func TestDaemonWiresTheViewKeeperToTheController(t *testing.T) {
	skipIfNoTmux(t)
	t.Setenv("HOME", t.TempDir())
	d, err := New()
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	if d.views == nil {
		t.Fatal("the daemon built no view keeper")
	}
	if d.views.OnMoved == nil {
		t.Error("Keeper.OnMoved is not wired: a moved view would tell no seat")
	}
	if d.views.Held == nil {
		t.Error("Keeper.Held is not wired: a view would never pause at the held-tree bound")
	}
	if d.teamCtrl.SealViewTrees == nil {
		t.Error("Controller.SealViewTrees is not wired: superseded trees would never be sealed")
	}
}
