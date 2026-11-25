package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/events"
)

// I-451 timing, store write (aae-orc-6layq, I-797 S5): one Record around
// bolt.Update, on the slice 1 histogram core, surfaced in DaemonStatus.Timings
// beside the rpc, tmux and reconcile sites. The site is read by its name, so
// these tests fail on an assertion and not on a build error.
const storeWriteSite = "store.write"

// boltStatusDaemon is statusDaemon over a daemon whose store has a bbolt file:
// the handler daemon's store is in memory, so no write reaches bolt.Update.
func boltStatusDaemon(t *testing.T) *Daemon {
	t.Helper()
	skipIfNoTmux(t)
	home, err := os.MkdirTemp("", "mst")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	t.Setenv(config.SocketEnv, "")
	d, err := NewWithOptions(Options{StateBolt: filepath.Join(home, "state.bolt")})
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	return d
}

func storeWriteCount(t *testing.T, d *Daemon) uint64 {
	t.Helper()
	return rpcSites(t, d)[storeWriteSite].Count
}

// One store write is one observation at the store-write site, and the status
// call carries it.
func TestStoreWriteIsTimedAndSurfacedInStatus(t *testing.T) {
	d := boltStatusDaemon(t)
	before := storeWriteCount(t, d)
	if err := d.store.CreateWorkspace(&api.Workspace{Name: "s5-timing"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if got := storeWriteCount(t, d) - before; got != 1 {
		t.Errorf("the %s site counted %d observations for one store write, want 1", storeWriteSite, got)
	}
	st := daemonStatus(t, d)
	if st.Timings == nil {
		t.Fatal("daemon.status has no timings")
	}
	var site int
	for _, s := range st.Timings.Sites {
		if s.Name == storeWriteSite {
			site++
			if s.Count == 0 || s.Max <= 0 {
				t.Errorf("the %s site in daemon.status has Count %d and Max %v, want an observation", storeWriteSite, s.Count, s.Max)
			}
		}
	}
	if site != 1 {
		t.Errorf("daemon.status lists the %s site %d times, want once", storeWriteSite, site)
	}
}

// A write over the site's budget is an overrun event, throttled to one per
// site per interval like the other sites: two slow writes, one event.
func TestStoreWriteOverrunIsThrottledLikeTheOtherSites(t *testing.T) {
	d := boltStatusDaemon(t)
	d.timing.SetBudget(storeWriteSite, time.Nanosecond)
	for _, name := range []string{"s5-slow-a", "s5-slow-b"} {
		if err := d.store.CreateWorkspace(&api.Workspace{Name: name}); err != nil {
			t.Fatalf("create workspace: %v", err)
		}
	}
	evs := d.events.Snapshot(events.Filter{Kind: events.KindDaemonOverrun}, 0)
	var n int
	for _, e := range evs {
		if e.Reason == storeWriteSite {
			n++
		}
	}
	if n != 1 {
		t.Errorf("overrun events for %s = %d, want 1 (two writes, throttled); events %+v", storeWriteSite, n, evs)
	}
	if got := rpcSites(t, d)[storeWriteSite].Overruns; got != 2 {
		t.Errorf("the %s site counted %d overruns, want 2 (the events are throttled, the count is not)", storeWriteSite, got)
	}
}
