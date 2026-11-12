package daemon

import (
	"os"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/procstat"
)

// The sampler pass is driven directly rather than through Start so the
// test does not wait out a tick. Its own pid stands in for a session's
// pane_pid: it is guaranteed to exist and to hold memory.
func TestSampleMetricsOnce(t *testing.T) {
	skipIfNoTmux(t)

	d, err := New()
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}

	live := &api.Session{
		Name:      "agent-0",
		Workspace: "metrics-ws",
		Team:      "squad",
		Role:      "worker",
		Runtime:   api.Runtime{Name: "sleep", Command: "sleep"},
		State:     api.SessionRunning,
		PID:       os.Getpid(),
	}
	noPID := &api.Session{
		Name:      "agent-1",
		Workspace: "metrics-ws",
		Team:      "squad",
		Role:      "worker",
		Runtime:   api.Runtime{Name: "sleep", Command: "sleep"},
		State:     api.SessionRunning,
	}
	for _, s := range []*api.Session{live, noPID} {
		if err := d.store.CreateSession(s); err != nil {
			t.Fatalf("create session %s: %v", s.Name, err)
		}
	}

	d.SampleMetricsOnce(procstat.NewSampler())

	got, err := d.store.GetSession("metrics-ws/agent-0")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.MetricsAt.IsZero() {
		t.Fatal("MetricsAt is zero after a sampler pass")
	}
	if got.RSSBytes <= 0 {
		t.Errorf("RSSBytes = %d for a live pid, want > 0", got.RSSBytes)
	}

	// A session with no pid must stay visibly unmeasured rather than
	// reporting zero usage.
	unmeasured, err := d.store.GetSession("metrics-ws/agent-1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if !unmeasured.MetricsAt.IsZero() {
		t.Error("a session with no PID was marked as measured")
	}
}

// A dead pid is measured and reports nothing, which is different from
// not being measured at all: the reconciler reaps the session shortly
// after, and until then the operator should see the zero.
func TestSampleMetricsOnceDeadPID(t *testing.T) {
	skipIfNoTmux(t)

	d, err := New()
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}

	// pid 1 exists; a pid just below the max is almost certainly free.
	// Any absent pid exercises the same branch.
	if err := d.store.CreateSession(&api.Session{
		Name:      "ghost",
		Workspace: "metrics-ws",
		Team:      "squad",
		Role:      "worker",
		Runtime:   api.Runtime{Name: "sleep", Command: "sleep"},
		State:     api.SessionRunning,
		PID:       0x7FFFFFF0,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	d.SampleMetricsOnce(procstat.NewSampler())

	got, err := d.store.GetSession("metrics-ws/ghost")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.MetricsAt.IsZero() {
		t.Fatal("an absent pid must still count as measured")
	}
	if got.RSSBytes != 0 || got.CPUPercent != 0 {
		t.Errorf("got %.1f%% CPU / %d bytes for an absent pid, want zero",
			got.CPUPercent, got.RSSBytes)
	}
}

// While the cluster is suspended the sampler writes nothing: the processes are
// stopped, so every reading would be 0% CPU. The first pass after the hold lifts
// only seeds the sampler's baseline and publishes nothing, because the CPU
// delta it would compute spans the whole pause; the pass after that publishes.
func TestMetricsPassIsHeldWhileSuspendedAndReseedsAfter(t *testing.T) {
	skipIfNoTmux(t)
	d, err := New()
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	s := &api.Session{
		Name: "agent-0", Workspace: "metrics-held", Team: "squad", Role: "worker",
		Runtime: api.Runtime{Name: "sleep", Command: "sleep"}, State: api.SessionRunning, PID: os.Getpid(),
	}
	if err := d.store.CreateSession(s); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.store.DeleteSession(s.Key()) })
	metricsAt := func() bool {
		got, err := d.store.GetSession(s.Key())
		if err != nil {
			t.Fatal(err)
		}
		return !got.MetricsAt.IsZero()
	}

	sampler := procstat.NewSampler()
	var held metricsHold
	d.suspendHeld.Store(true)
	d.metricsPass(sampler, &held)
	d.metricsPass(sampler, &held)
	if metricsAt() {
		t.Fatal("a pass wrote metrics while the cluster was suspended")
	}

	d.suspendHeld.Store(false)
	d.metricsPass(sampler, &held)
	if metricsAt() {
		t.Fatal("the first pass after the hold lifted published a reading across the pause")
	}
	d.metricsPass(sampler, &held)
	if !metricsAt() {
		t.Fatal("the second pass after the hold lifted did not publish")
	}
}

// The stored reading carries CPU in millicpu beside the percent, which keeps its meaning.
func TestMetricsFromCarriesMillicpu(t *testing.T) {
	got := metricsFrom(procstat.Sample{CPUPercent: 968.1, RSSBytes: 7})
	if got.Millicpu != 9681 {
		t.Errorf("Millicpu = %d, want 9681", got.Millicpu)
	}
	if got.CPUPercent != 968.1 || got.RSSBytes != 7 {
		t.Errorf("CPUPercent = %v, RSSBytes = %d, want 968.1 and 7", got.CPUPercent, got.RSSBytes)
	}
}
