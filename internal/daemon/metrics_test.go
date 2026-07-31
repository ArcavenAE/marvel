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
