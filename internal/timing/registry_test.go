package timing

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/events"
)

func overruns(r *events.Ring) []events.Event {
	return r.Snapshot(events.Filter{Kind: events.KindDaemonOverrun}, 0)
}

func TestRecordKeepsAHistogramPerSite(t *testing.T) {
	t.Parallel()
	reg := NewRegistry(events.NewRing(16), newClock().now)
	reg.SetBudget("rpc.apply", 2*time.Second)
	reg.Record("rpc.apply", 10*time.Millisecond)
	reg.Record("rpc.apply", 20*time.Millisecond)
	reg.Record("rpc.get", 5*time.Millisecond)
	rep := reg.Report()
	if len(rep.Sites) != 2 {
		t.Fatalf("Report has %d sites, want 2: %+v", len(rep.Sites), rep.Sites)
	}
	if rep.Sites[0].Name != "rpc.apply" || rep.Sites[1].Name != "rpc.get" {
		t.Fatalf("sites = %q, %q, want rpc.apply then rpc.get (sorted by name)", rep.Sites[0].Name, rep.Sites[1].Name)
	}
	apply := rep.Sites[0]
	if apply.Count != 2 || apply.Sum != 30*time.Millisecond || apply.Budget != 2*time.Second {
		t.Errorf("rpc.apply Count/Sum/Budget = %d/%s/%s, want 2/30ms/2s", apply.Count, apply.Sum, apply.Budget)
	}
	if got := rep.Sites[1].Count; got != 1 {
		t.Errorf("rpc.get Count = %d, want 1", got)
	}
}

func TestAddKeepsANamedCounter(t *testing.T) {
	t.Parallel()
	reg := NewRegistry(nil, newClock().now)
	reg.Add(CounterMissedTicks, 3)
	reg.Add(CounterMissedTicks, 4)
	if got := reg.Report().Counters[CounterMissedTicks]; got != 7 {
		t.Errorf("%s = %d, want 7", CounterMissedTicks, got)
	}
}

func TestOverrunEmitsAWarningThatNamesTheSite(t *testing.T) {
	t.Parallel()
	ring := events.NewRing(16)
	reg := NewRegistry(ring, newClock().now)
	reg.SetBudget(SiteReconcilePass, 100*time.Millisecond)
	reg.Record(SiteReconcilePass, 40*time.Millisecond) // inside the budget: no event
	if n := len(overruns(ring)); n != 0 {
		t.Fatalf("a call inside its budget emitted %d overrun events", n)
	}
	reg.Record(SiteReconcilePass, 300*time.Millisecond)
	evs := overruns(ring)
	if len(evs) != 1 {
		t.Fatalf("an overrun emitted %d events, want 1", len(evs))
	}
	ev := evs[0]
	if ev.Severity != events.SeverityWarning || ev.Reason != SiteReconcilePass {
		t.Errorf("severity/reason = %s/%q, want warning/%q", ev.Severity, ev.Reason, SiteReconcilePass)
	}
	for _, want := range []string{"300ms", "100ms"} {
		if !strings.Contains(ev.Message, want) {
			t.Errorf("message %q does not name %s (the duration and the budget)", ev.Message, want)
		}
	}
	if strings.Contains(ev.Message, "suppressed") {
		t.Errorf("message %q mentions suppressed on a first event", ev.Message)
	}
}

func TestOverrunEventThrottled(t *testing.T) {
	t.Parallel()
	clk := newClock()
	ring := events.NewRing(32)
	reg := NewRegistry(ring, clk.now)
	reg.SetBudget(SiteReconcilePass, 100*time.Millisecond)
	reg.SetBudget(RPCSite("apply"), time.Second)
	for range 3 {
		reg.Record(SiteReconcilePass, 300*time.Millisecond)
		clk.advance(time.Second)
	}
	if n := len(overruns(ring)); n != 1 {
		t.Fatalf("3 overruns inside 10 minutes emitted %d events, want 1", n)
	}
	if got := reg.Report().Sites[0].Overruns; got != 3 {
		t.Errorf("Overruns = %d, want 3: the throttle holds back events, never the count", got)
	}
	// Another site is not held by this one's throttle.
	reg.Record(RPCSite("apply"), 5*time.Second)
	if n := len(overruns(ring)); n != 2 {
		t.Fatalf("an overrun at a second site emitted %d events in all, want 2", n)
	}
	clk.advance(OverrunEventInterval)
	reg.Record(SiteReconcilePass, 300*time.Millisecond)
	evs := overruns(ring)
	if len(evs) != 3 {
		t.Fatalf("an overrun after the interval left %d events in all, want 3", len(evs))
	}
	if last := evs[2]; !strings.Contains(last.Message, "suppressed 2") {
		t.Errorf("message %q does not carry the suppressed count (want \"suppressed 2\")", last.Message)
	}
}

func TestANilRegistryRecordsNothing(t *testing.T) {
	t.Parallel()
	var reg *Registry
	reg.SetBudget("x", time.Second)
	reg.Record("x", time.Hour)
	reg.Add("y", 1)
	if rep := reg.Report(); len(rep.Sites) != 0 || len(rep.Counters) != 0 {
		t.Errorf("a nil registry reported %+v", rep)
	}
}
