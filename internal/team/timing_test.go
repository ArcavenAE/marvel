package team

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/timing"
)

// slowTmux puts a tmux that answers success after delay first on PATH, under a
// scratch tmux socket name, so a reconcile pass runs through the real Driver
// and the real tmux server is never reached. Call it before setup.
func slowTmux(t *testing.T, delay string) {
	t.Helper()
	dir := t.TempDir()
	body := "#!/bin/sh\nsleep " + delay + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MARVEL_TMUX_SOCKET", "timing-"+filepath.Base(dir))
}

func oneSleepRole() []api.Role {
	return []api.Role{{Name: "r", Replicas: 1, Runtime: api.Runtime{Command: "sleep", Args: []string{"30"}}}}
}

func TestReconcileOverrunEmitsEvent(t *testing.T) {
	// 0.2s a call, so even a pass of a single tmux call clears the 100ms budget.
	slowTmux(t, "0.2")
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ring := events.NewRing(64)
	reg := timing.NewRegistry(ring, time.Now)
	reg.SetBudget(timing.SiteReconcilePass, 100*time.Millisecond)
	ctrl.Timing = reg
	createTeamFixture(t, store, "ws", "tm", oneSleepRole())

	ctrl.ReconcileOnce()

	evs := ring.Snapshot(events.Filter{Kind: events.KindDaemonOverrun}, 0)
	if len(evs) != 1 {
		t.Fatalf("a pass of about a second against a 100ms budget emitted %d overrun events, want 1", len(evs))
	}
	if evs[0].Reason != timing.SiteReconcilePass || evs[0].Severity != events.SeverityWarning {
		t.Errorf("event reason/severity = %q/%s, want %q/warning", evs[0].Reason, evs[0].Severity, timing.SiteReconcilePass)
	}
	var pass timing.SiteReport
	for _, s := range reg.Report().Sites {
		if s.Name == timing.SiteReconcilePass {
			pass = s
		}
	}
	if pass.Count != 1 || pass.Overruns != 1 || pass.Max < 100*time.Millisecond {
		t.Errorf("reconcile.pass Count/Overruns/Max = %d/%d/%s, want 1/1/at least 100ms", pass.Count, pass.Overruns, pass.Max)
	}
}

func TestMissedTicksCounted(t *testing.T) {
	slowTmux(t, "0.05")
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	reg := timing.NewRegistry(nil, time.Now)
	ctrl.Timing = reg
	createTeamFixture(t, store, "ws", "tm", oneSleepRole())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		// A 50ms interval against a pass of about a second: the ticker drops
		// most of the ticks that fall inside the first pass.
		ctrl.Run(ctx, 50*time.Millisecond)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for reg.Report().Counters[timing.CounterMissedTicks] < 5 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	<-done
	if got := reg.Report().Counters[timing.CounterMissedTicks]; got < 5 {
		t.Errorf("%s = %d after a pass many intervals long, want at least 5", timing.CounterMissedTicks, got)
	}
}
