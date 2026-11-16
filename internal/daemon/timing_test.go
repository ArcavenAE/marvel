package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/timing"
)

func rpcSites(t *testing.T, d *Daemon) map[string]timing.SiteReport {
	t.Helper()
	out := map[string]timing.SiteReport{}
	for _, s := range d.timing.Report().Sites {
		out[s.Name] = s
	}
	return out
}

func TestDispatchRecordsPerMethod(t *testing.T) {
	d := statusDaemon(t)
	d.dispatchAs(daemonrpc.Request{Method: "daemon.status"}, localCaller())
	d.dispatchAs(daemonrpc.Request{Method: "daemon.status"}, localCaller())
	d.dispatchAs(daemonrpc.Request{Method: "bus.status"}, localCaller())

	sites := rpcSites(t, d)
	if got := sites[timing.RPCSite("daemon.status")].Count; got != 2 {
		t.Errorf("rpc.daemon.status Count = %d, want 2", got)
	}
	if got := sites[timing.RPCSite("bus.status")].Count; got != 1 {
		t.Errorf("rpc.bus.status Count = %d, want 1", got)
	}
}

// A method name a client invents must not mint a site of its own: the number
// of sites stays bounded by the methods the daemon serves.
func TestDispatchBucketsAnUnknownMethodTogether(t *testing.T) {
	d := statusDaemon(t)
	d.dispatchAs(daemonrpc.Request{Method: "no-such-method-a"}, localCaller())
	d.dispatchAs(daemonrpc.Request{Method: "no-such-method-b"}, localCaller())

	sites := rpcSites(t, d)
	if got := sites[timing.RPCSite("other")].Count; got != 2 {
		t.Errorf("rpc.other Count = %d, want 2", got)
	}
	for name := range sites {
		if name == timing.RPCSite("no-such-method-a") || name == timing.RPCSite("no-such-method-b") {
			t.Errorf("an unknown method minted the site %q", name)
		}
	}
}

func TestDaemonStatusCarriesTimings(t *testing.T) {
	d := statusDaemon(t)
	d.dispatchAs(daemonrpc.Request{Method: "bus.status"}, localCaller())
	d.timing.Add(timing.CounterMissedTicks, 4)

	st := daemonStatus(t, d)
	if st.Timings == nil {
		t.Fatal("daemon.status has no timings")
	}
	var bus timing.SiteReport
	for _, s := range st.Timings.Sites {
		if s.Name == timing.RPCSite("bus.status") {
			bus = s
		}
	}
	if bus.Count != 1 {
		t.Errorf("timings rpc.bus.status Count = %d, want 1", bus.Count)
	}
	if got := st.Timings.Counters[timing.CounterMissedTicks]; got != 4 {
		t.Errorf("timings %s = %d, want 4", timing.CounterMissedTicks, got)
	}
	// The status call must survive a round trip through JSON, which is how
	// describe daemon receives it.
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	var back DaemonStatus
	if err := json.Unmarshal(b, &back); err != nil || back.Timings == nil {
		t.Errorf("timings did not survive a JSON round trip: err %v, timings %v", err, back.Timings)
	}
}

func TestDaemonWiresOverrunEventsToTheRing(t *testing.T) {
	d := statusDaemon(t)
	d.timing.SetBudget(timing.RPCSite("bus.status"), time.Nanosecond)
	d.dispatchAs(daemonrpc.Request{Method: "bus.status"}, localCaller())

	evs := d.events.Snapshot(events.Filter{Kind: events.KindDaemonOverrun}, 0)
	if len(evs) != 1 || evs[0].Reason != timing.RPCSite("bus.status") {
		t.Errorf("overrun events = %+v, want one for rpc.bus.status", evs)
	}
}
