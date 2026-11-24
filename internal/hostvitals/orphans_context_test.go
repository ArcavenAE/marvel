package hostvitals

import (
	"reflect"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/procargs"
)

// Item 5, option (c): each orphan row shows its parent chain and its start time.
func TestAnOrphanShowsItsParentChainAndStartTime(t *testing.T) {
	r := newRig(t)
	r.roots = []int{100}
	r.pass(op(50, 1, "term", 1, 900), op(100, 50, "sh", 1, 600), op(101, 100, "worker", 10, 600))
	r.pass(op(50, 1, "term", 1, 960), op(100, 50, "sh", 1, 660), op(101, 1, "worker", 12, 660))
	got := r.orphans()
	if len(got) != 1 {
		t.Fatalf("orphans = %+v, want one", got)
	}
	if want := []string{"sh", "term"}; !reflect.DeepEqual(got[0].Parents, want) {
		t.Errorf("Parents = %v, want %v", got[0].Parents, want)
	}
	if want := r.now.Add(-660 * time.Second); !got[0].StartedAt.Equal(want) {
		t.Errorf("StartedAt = %v, want %v", got[0].StartedAt, want)
	}
}

// A process first seen already parentless was never seen with a parent.
func TestAMarkedOrphanNeverSeenWithAParentHasNoChain(t *testing.T) {
	r := newRig(t)
	r.markers[200] = procargs.Marker{Session: "ws/seat-x"}
	r.pass(op(200, 1, "modeld", 5, 3000))
	got := r.orphans()
	if len(got) != 1 || len(got[0].Parents) != 0 || got[0].StartedAt.IsZero() {
		t.Errorf("orphans = %+v, want one with no parents and a start time", got)
	}
}

// Item 5, option (b): a host-config list of bundle or executable paths that
// are never counted as orphans.
func excludeRig(t *testing.T, exclude []string, args map[int][]string) *orphanRig {
	r := newRig(t)
	r.roots = []int{100}
	r.s.SetOrphanSources(OrphanSources{
		Roots:       func() []int { return r.roots },
		RootSession: func(pid int) string { return r.seat[pid] },
		Detail:      func(pid int) ProcDetail { return ProcDetail{Args: args[pid], Cwd: "/fixture"} },
		Exclude:     exclude,
	})
	return r
}

func TestAnExcludedBundleIsNeverAnOrphan(t *testing.T) {
	args := map[int][]string{
		101: {"/fixture/Apps/Vm.app/Contents/MacOS/vm", "--run"},
		102: {"/fixture/bin/leftover", "--run"},
	}
	r := excludeRig(t, []string{"/fixture/Apps/Vm.app"}, args)
	r.pass(op(100, 50, "sh", 1, 600), op(101, 100, "vm", 10, 600), op(102, 100, "leftover", 10, 600))
	r.pass(op(100, 50, "sh", 1, 660), op(101, 1, "vm", 12, 660), op(102, 1, "leftover", 12, 660))
	got := r.orphans()
	if len(got) != 1 || got[0].PID != 102 {
		t.Errorf("orphans = %+v, want only pid 102 (pid 101 is under an excluded bundle)", got)
	}
}

func TestAnExcludeEntryMatchesWholePathElementsOnly(t *testing.T) {
	args := map[int][]string{101: {"/fixture/Apps/Vm.app2/Contents/MacOS/vm"}}
	r := excludeRig(t, []string{"/fixture/Apps/Vm.app"}, args)
	r.pass(op(100, 50, "sh", 1, 600), op(101, 100, "vm", 10, 600))
	r.pass(op(100, 50, "sh", 1, 660), op(101, 1, "vm", 12, 660))
	if got := r.orphans(); len(got) != 1 || got[0].PID != 101 {
		t.Errorf("orphans = %+v, want pid 101 listed: Vm.app2 is not Vm.app", got)
	}
}

func TestAnEmptyExcludeListExcludesNothing(t *testing.T) {
	args := map[int][]string{101: {"/fixture/Apps/Vm.app/Contents/MacOS/vm"}}
	r := excludeRig(t, nil, args)
	r.pass(op(100, 50, "sh", 1, 600), op(101, 100, "vm", 10, 600))
	r.pass(op(100, 50, "sh", 1, 660), op(101, 1, "vm", 12, 660))
	if got := r.orphans(); len(got) != 1 {
		t.Errorf("orphans = %+v, want pid 101 listed with an empty exclude list", got)
	}
}
