package session

import (
	"os"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/tmux"
)

// The reap path's exit-status mapping (aae-orc-bxeh, ADR-010), through a
// real tmux server and marvel's own launch form. Three sessions, three
// verdicts:
//
//   - headless, exit 0: Succeeded, exit status recorded, NOT reaped (no
//     crash charge), dead pane reclaimed, session.succeeded emitted;
//   - headless, exit 3: Crashed as before, exit status recorded, reaped;
//   - interactive, exit 0: Crashed as before, window closed so nothing to
//     record, reaped.
//
// The completed run must then hold its replica slot, which is the
// arithmetic planRole runs (CountReplicaSlots) and the reason the churn
// finding-036 measured stops.
func TestReapDeadMapsExitStatus(t *testing.T) {
	skipIfNoTmux(t)

	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	mgr := NewManager(store, driver)
	ring := events.NewRing(32)
	mgr.Events = ring

	ws := "test-reap-exit-status"
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })

	create := func(name string, mode api.RuntimeMode, script string) *api.Session {
		t.Helper()
		sess := &api.Session{
			Name:      name,
			Workspace: ws,
			Team:      "jobs",
			Role:      name,
			// The direct launch path joins Args as shell text, so the
			// quoting is the caller's: without it `sh -c exit 3` runs
			// `exit` with $0=3 and exits 0.
			Runtime: api.Runtime{
				Name: "sh", Command: "sh", Args: []string{"-c", "'" + script + "'"},
				Mode: mode,
			},
		}
		if err := mgr.Create(sess); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return sess
	}
	done := create("done", api.RuntimeModeHeadless, "exit 0")
	failed := create("failed", api.RuntimeModeHeadless, "exit 3")
	// The interactive window is switched back to close-on-exit after
	// new-window returns; a command that exits inside that gap persists
	// dead with its status (consequence 4, benign direction) and would
	// read "crashed (exit 0)". The sleep keeps this case on the
	// window-closed path the assertion below is about.
	gone := create("gone", api.RuntimeModeInteractive, "sleep 1; exit 0")

	// Every pane has exited: the kept ones read dead, the closed one is
	// gone. Poll rather than sleep so the test does not race tmux.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		a, _ := driver.PaneStatus(done.PaneID)
		b, _ := driver.PaneStatus(failed.PaneID)
		c, _ := driver.PaneStatus(gone.PaneID)
		if a.Dead && b.Dead && !c.Exists {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	reaped := mgr.ReapDead()
	keys := map[string]bool{}
	for _, r := range reaped {
		keys[r.Key] = true
	}
	if keys[done.Key()] {
		t.Fatalf("completed headless run %s was reaped as a crash: %v", done.Key(), reaped)
	}
	if !keys[failed.Key()] || !keys[gone.Key()] {
		t.Fatalf("expected %s and %s reaped, got %v", failed.Key(), gone.Key(), reaped)
	}

	got, err := store.GetSession(done.Key())
	if err != nil {
		t.Fatalf("completed run must stay in the store: %v", err)
	}
	if got.State != api.SessionSucceeded || got.ExitStatus != "0" || got.PaneID != "" {
		t.Fatalf("completed run = state %s exit %q pane %q, want succeeded / 0 / cleared", got.State, got.ExitStatus, got.PaneID)
	}
	if got.HealthState != api.HealthUnknown {
		t.Fatalf("completed run health = %s, want unknown (no process, no verdict)", got.HealthState)
	}
	if !api.OccupiesReplicaSlot(got) || got.State.CountsAsAlive() {
		t.Fatalf("completed run must hold its slot without reading alive: slot=%v alive=%v",
			api.OccupiesReplicaSlot(got), got.State.CountsAsAlive())
	}
	if st, _ := driver.PaneStatus(done.PaneID); st.Exists {
		t.Fatalf("dead pane %s was not reclaimed after its status was read", done.PaneID)
	}

	got, err = store.GetSession(failed.Key())
	if err != nil {
		t.Fatalf("failed run must stay in the store as a crash marker: %v", err)
	}
	if got.State != api.SessionCrashed || got.ExitStatus != "3" || got.HealthState != api.HealthUnhealthy {
		t.Fatalf("failed run = state %s exit %q health %s, want crashed / 3 / unhealthy", got.State, got.ExitStatus, got.HealthState)
	}
	if api.OccupiesReplicaSlot(got) {
		t.Fatalf("a failed headless run must not hold its slot (a broken role must be replaced)")
	}

	got, err = store.GetSession(gone.Key())
	if err != nil {
		t.Fatalf("interactive session must stay in the store as a crash marker: %v", err)
	}
	if got.State != api.SessionCrashed || got.ExitStatus != "" {
		t.Fatalf("interactive exit = state %s exit %q, want crashed with no status (window closed)", got.State, got.ExitStatus)
	}

	succeeded := ring.Snapshot(events.Filter{Kind: events.KindSessionSucceeded}, 0)
	if len(succeeded) != 1 || succeeded[0].Session != done.Key() {
		t.Fatalf("session.succeeded events = %+v, want exactly one for %s", succeeded, done.Key())
	}
	crashed := ring.Snapshot(events.Filter{Kind: events.KindSessionCrashed}, 0)
	if len(crashed) != 2 {
		t.Fatalf("session.crashed events = %d, want 2 (failed + interactive)", len(crashed))
	}

	// A second pass changes nothing: the completed run is skipped (no
	// pane), not re-read, not re-charged.
	if again := mgr.ReapDead(); len(again) != 0 {
		t.Fatalf("second ReapDead reaped %v, want nothing", again)
	}
}

// An empty exit status is UNKNOWN, never success. It is how tmux reports
// death by signal, and below tmux 3.5 it is also how a lost status reads.
// A headless run killed mid-flight must therefore be charged as a crash
// and replaced, not parked as a satisfied replica.
func TestReapDeadEmptyStatusIsNotCompletion(t *testing.T) {
	skipIfNoTmux(t)

	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	mgr := NewManager(store, driver)

	ws := "test-reap-signal"
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })

	sess := &api.Session{
		Name: "killed", Workspace: ws, Team: "jobs", Role: "killed",
		Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}, Mode: api.RuntimeModeHeadless},
	}
	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create: %v", err)
	}
	pid, err := driver.PanePID(sess.PaneID)
	if err != nil {
		t.Fatalf("pane pid: %v", err)
	}
	killProcess(t, pid)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if st, _ := driver.PaneStatus(sess.PaneID); st.Dead {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	reaped := mgr.ReapDead()
	if len(reaped) != 1 || reaped[0].Key != sess.Key() {
		t.Fatalf("killed headless run must be reaped as a crash, got %v", reaped)
	}
	got, _ := store.GetSession(sess.Key())
	if got.State != api.SessionCrashed || got.ExitStatus != "" {
		t.Fatalf("killed run = state %s exit %q, want crashed with empty status", got.State, got.ExitStatus)
	}
}

// killProcess sends SIGKILL to the pane's shell process. tmux runs the
// command through sh -c, and sh execs a single simple command, so the
// pane pid IS the sleep here and the pane dies by signal.
func killProcess(t *testing.T, pid int) {
	t.Helper()
	p, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("find process %d: %v", pid, err)
	}
	if err := p.Kill(); err != nil {
		t.Fatalf("kill %d: %v", pid, err)
	}
}
