package session

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/tmux"
)

func skipIfNoTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
}

func TestSessionCreateDelete(t *testing.T) {
	skipIfNoTmux(t)

	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	mgr := NewManager(store, driver)

	ws := "test-sess-mgr"
	t.Cleanup(func() {
		_ = mgr.CleanupWorkspace(ws)
	})

	sess := &api.Session{
		Name:      "agent-0",
		Workspace: ws,
		Team:      "agents",
		Role:      "worker",
		Runtime:   api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
	}

	// Create
	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if sess.State != api.SessionRunning {
		t.Fatalf("expected running, got %s", sess.State)
	}
	if sess.PaneID == "" {
		t.Fatal("expected pane ID")
	}

	// Verify in store
	got, err := store.GetSession(ws + "/agent-0")
	if err != nil {
		t.Fatalf("get from store: %v", err)
	}
	if got.PaneID != sess.PaneID {
		t.Fatalf("store pane ID mismatch")
	}

	// Delete
	if err := mgr.Delete(ws + "/agent-0"); err != nil {
		t.Fatalf("delete session: %v", err)
	}

	// Verify gone from store
	sessions := store.ListSessions()
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions after delete, got %d", len(sessions))
	}
}

func TestCleanupWorkspace(t *testing.T) {
	skipIfNoTmux(t)

	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	mgr := NewManager(store, driver)

	ws := "test-cleanup"
	t.Cleanup(func() {
		_ = mgr.CleanupWorkspace(ws)
	})

	// Create two sessions
	for _, name := range []string{"w-0", "w-1"} {
		sess := &api.Session{
			Name:      name,
			Workspace: ws,
			Team:      "agents",
			Role:      "worker",
			Runtime:   api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
		}
		if err := mgr.Create(sess); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	if len(store.ListSessions()) != 2 {
		t.Fatal("expected 2 sessions")
	}

	// Cleanup
	if err := mgr.CleanupWorkspace(ws); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	if len(store.ListSessions()) != 0 {
		t.Fatal("expected 0 sessions after cleanup")
	}

	if driver.HasSession("marvel-" + ws) {
		t.Fatal("tmux session should be gone after cleanup")
	}
}

func TestAdoptOrKill_PrefixOnly(t *testing.T) {
	skipIfNoTmux(t)

	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}

	// Use a unique prefix so we do not step on other tmux-using tests
	// (e.g. TestDaemonLifecycle) that may be running in parallel
	// packages and use the real marvel- prefix.
	prefix := "marvel-orphantest-"
	orphans := []string{prefix + "a", prefix + "b"}
	outsider := "not-" + prefix + "survivor"

	for _, name := range []string{orphans[0], orphans[1], outsider} {
		if err := driver.NewSession(name); err != nil {
			t.Fatalf("new session %s: %v", name, err)
		}
	}
	t.Cleanup(func() {
		for _, n := range append(orphans, outsider) {
			_ = driver.KillSession(n)
		}
	})

	// Empty store + AdoptOrKill = kill-all under the prefix (the
	// degenerate-mode safety net from finding-050 / aae-orc-k4e4
	// Session 2). Outsider sessions are untouched.
	mgr := NewManager(api.NewStore(), driver)
	if _, _, err := mgr.adoptOrKillPrefix(prefix); err != nil {
		t.Fatalf("adoptOrKillPrefix: %v", err)
	}

	for _, name := range orphans {
		if driver.HasSession(name) {
			t.Fatalf("prefixed session %s should have been killed (no recorded workspace)", name)
		}
	}
	if !driver.HasSession(outsider) {
		t.Fatalf("non-prefix session %s must not be touched", outsider)
	}
}

// A fleet kill has to reach the event ring, not only the log. Before
// aae-orc-kvcs it reached neither in the collected artifact set: the
// killing daemon's log carried the line, `marvel events` was blank, and
// an independent reader given the victim's artifacts misattributed a
// marvel-caused kill to the environment.
func TestAdoptOrKillEmitsKilledEventNamingTheActor(t *testing.T) {
	skipIfNoTmux(t)

	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}

	prefix := "marvel-killevent-"
	victim := prefix + "alpha"
	if err := driver.NewSession(victim); err != nil {
		t.Fatalf("new session %s: %v", victim, err)
	}
	t.Cleanup(func() { _ = driver.KillSession(victim) })

	ring := events.NewRing(10)
	mgr := NewManager(api.NewStore(), driver)
	mgr.Events = ring
	mgr.SocketPath = "/tmp/marvel-killevent.sock"

	if _, killed, err := mgr.adoptOrKillPrefix(prefix); err != nil {
		t.Fatalf("adoptOrKillPrefix: %v", err)
	} else if killed != 1 {
		t.Fatalf("killed = %d, want 1", killed)
	}

	got := ring.Snapshot(events.Filter{Kind: events.KindReconcileKilled}, 0)
	if len(got) != 1 {
		t.Fatalf("reconcile.killed events = %d, want 1", len(got))
	}
	ev := got[0]
	if ev.Severity != events.SeverityWarning {
		t.Errorf("severity = %q, want %q", ev.Severity, events.SeverityWarning)
	}
	if ev.Workspace != "alpha" {
		t.Errorf("workspace = %q, want %q", ev.Workspace, "alpha")
	}
	// The actor is the whole point: with two daemons appending to one
	// log file, an event that does not name the process that acted
	// cannot be traced back to it.
	wantActor := fmt.Sprintf("pid=%d socket=/tmp/marvel-killevent.sock", os.Getpid())
	if ev.Actor != wantActor {
		t.Errorf("actor = %q, want %q", ev.Actor, wantActor)
	}
	if !strings.Contains(ev.Message, victim) {
		t.Errorf("message %q does not name the killed session %q", ev.Message, victim)
	}
}

// An adopted pane emits too, so the ring shows both halves of a
// reconcile pass and an operator can tell "adopted mine" from "killed
// something else's" without reading the log.
func TestAdoptOrKillEmitsAdoptedEvent(t *testing.T) {
	skipIfNoTmux(t)

	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	mgr := NewManager(store, driver)

	ws := "test-adopt-event"
	if err := store.CreateWorkspace(&api.Workspace{Name: ws}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	sess := &api.Session{
		Name:      "t-r-g1-0",
		Workspace: ws,
		Team:      "t",
		Role:      "r",
		Runtime:   api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
	}
	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })

	ring := events.NewRing(10)
	mgr.Events = ring

	if adopted, _, err := mgr.AdoptOrKill(); err != nil {
		t.Fatalf("AdoptOrKill: %v", err)
	} else if adopted < 1 {
		t.Fatalf("adopted = %d, want at least 1", adopted)
	}

	got := ring.Snapshot(events.Filter{Kind: events.KindReconcileAdopted}, 0)
	if len(got) < 1 {
		t.Fatalf("reconcile.adopted events = %d, want at least 1", len(got))
	}
	if got[0].Actor == "" {
		t.Error("adopted event has no actor")
	}
	if got[0].Session != sess.Key() {
		t.Errorf("session = %q, want %q", got[0].Session, sess.Key())
	}
}

// actorID omits the socket half rather than rendering it blank, so a
// Manager driven directly by a test still produces a usable identity.
func TestActorIDOmitsUnsetSocket(t *testing.T) {
	mgr := NewManager(api.NewStore(), nil)

	if got, want := mgr.actorID(), fmt.Sprintf("pid=%d", os.Getpid()); got != want {
		t.Errorf("actorID with no socket = %q, want %q", got, want)
	}

	mgr.SocketPath = "/run/user/501/marvel/marvel.sock"
	want := fmt.Sprintf("pid=%d socket=/run/user/501/marvel/marvel.sock", os.Getpid())
	if got := mgr.actorID(); got != want {
		t.Errorf("actorID with socket = %q, want %q", got, want)
	}
}

// The ratified default: err on silent accumulation, not silent
// destruction (2026-08-07). Before this, a plain `marvel daemon` killed
// every marvel-* session it did not have records for, which destroyed a
// second daemon's entire running fleet on an ordinary action.
func TestAdoptOrLeaveLeavesUnrecordedStateRunning(t *testing.T) {
	skipIfNoTmux(t)

	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}

	prefix := "marvel-leavetest-"
	other := prefix + "someone-elses-fleet"
	if err := driver.NewSession(other); err != nil {
		t.Fatalf("new session %s: %v", other, err)
	}
	t.Cleanup(func() { _ = driver.KillSession(other) })

	ring := events.NewRing(10)
	mgr := NewManager(api.NewStore(), driver)
	mgr.Events = ring

	_, left, err := mgr.reconcilePrefix(prefix, LeaveUnrecorded)
	if err != nil {
		t.Fatalf("reconcilePrefix: %v", err)
	}
	if left != 1 {
		t.Fatalf("left = %d, want 1", left)
	}
	if !driver.HasSession(other) {
		t.Fatal("unrecorded session was destroyed; the default must leave it running")
	}

	// Left alone must not mean unreported, or the ruling trades one
	// silent failure for another.
	got := ring.Snapshot(events.Filter{Kind: events.KindReconcileLeft}, 0)
	if len(got) != 1 {
		t.Fatalf("reconcile.left events = %d, want 1", len(got))
	}
	if got[0].Severity != events.SeverityWarning {
		t.Errorf("severity = %q, want %q", got[0].Severity, events.SeverityWarning)
	}
	if got[0].Actor == "" {
		t.Error("left event has no actor")
	}
	if killed := ring.Snapshot(events.Filter{Kind: events.KindReconcileKilled}, 0); len(killed) != 0 {
		t.Errorf("emitted %d kill events under the leave policy", len(killed))
	}
}

// Leaving other daemons' state alone must not cost adoption of our own,
// which is what makes restart-without-agent-loss work.
func TestAdoptOrLeaveStillAdoptsRecordedPanes(t *testing.T) {
	skipIfNoTmux(t)

	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	mgr := NewManager(store, driver)

	ws := "test-adopt-or-leave"
	if err := store.CreateWorkspace(&api.Workspace{Name: ws}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	sess := &api.Session{
		Name:      "t-r-g1-0",
		Workspace: ws,
		Team:      "t",
		Role:      "r",
		Runtime:   api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
	}
	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })

	adopted, _, err := mgr.AdoptOrLeave()
	if err != nil {
		t.Fatalf("AdoptOrLeave: %v", err)
	}
	if adopted < 1 {
		t.Fatalf("adopted = %d, want at least 1", adopted)
	}
	if !driver.HasSession("marvel-" + ws) {
		t.Fatal("our own workspace session went away")
	}
}

// The reap preview and the reap action are computed the same way, so
// what an operator is shown is what they lose. A preview that could
// disagree with the action would put the surprise back.
func TestUnrecordedTmuxStateMatchesWhatKillWouldDestroy(t *testing.T) {
	skipIfNoTmux(t)

	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}

	orphan := "marvel-reappreview"
	if err := driver.NewSession(orphan); err != nil {
		t.Fatalf("new session: %v", err)
	}
	t.Cleanup(func() { _ = driver.KillSession(orphan) })

	mgr := NewManager(api.NewStore(), driver)

	found, err := mgr.UnrecordedTmuxState()
	if err != nil {
		t.Fatalf("UnrecordedTmuxState: %v", err)
	}
	var named bool
	for _, f := range found {
		if strings.Contains(f, orphan) {
			named = true
		}
	}
	if !named {
		t.Fatalf("preview does not name the orphan %q: %v", orphan, found)
	}

	// The preview is read-only. Calling it must not change anything.
	if !driver.HasSession(orphan) {
		t.Fatal("UnrecordedTmuxState destroyed something; it must only look")
	}
}

func TestReapDead(t *testing.T) {
	skipIfNoTmux(t)

	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	mgr := NewManager(store, driver)

	ws := "test-reap-dead"
	t.Cleanup(func() {
		_ = mgr.CleanupWorkspace(ws)
	})

	// Two live sessions plus a bookkeeping session whose pane we'll kill
	// out-of-band so ReapDead has something to clear.
	for _, name := range []string{"live-0", "dying-0"} {
		sess := &api.Session{
			Name:      name,
			Workspace: ws,
			Team:      "agents",
			Role:      "worker",
			Runtime:   api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
		}
		if err := mgr.Create(sess); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	dying, err := store.GetSession(ws + "/dying-0")
	if err != nil {
		t.Fatalf("get dying-0: %v", err)
	}
	// Kill the pane behind the manager's back — simulates a runtime
	// process that crashed or a tmux window the user closed manually.
	// tmux processes the kill-pane asynchronously; poll HasPane until
	// it reports the pane gone (or give up) before we ReapDead, so the
	// test isn't racing the tmux server.
	if err := driver.KillPane(dying.PaneID); err != nil {
		t.Fatalf("kill pane %s: %v", dying.PaneID, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for driver.HasPane(dying.PaneID) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if driver.HasPane(dying.PaneID) {
		t.Fatalf("tmux still reports pane %s alive after kill-pane", dying.PaneID)
	}

	reaped := mgr.ReapDead()
	if len(reaped) != 1 || reaped[0].Key != dying.Key() {
		t.Fatalf("expected ReapDead to return [%s], got %v", dying.Key(), reaped)
	}
	if got := reaped[0]; got.Workspace != ws || got.Team != "agents" || got.Role != "worker" {
		t.Fatalf("reaped session identity wrong: %+v", got)
	}
	// Session is kept in the store as a Crashed marker so operators see
	// the event via `marvel get sessions`. ReapDead no longer deletes.
	got, err := store.GetSession(dying.Key())
	if err != nil {
		t.Fatalf("expected dying session to stay in store as Crashed marker: %v", err)
	}
	if got.State != api.SessionCrashed {
		t.Fatalf("expected state=%s, got %s", api.SessionCrashed, got.State)
	}
	if got.PaneID != "" {
		t.Fatalf("expected PaneID cleared on crash, got %q", got.PaneID)
	}
	if _, err := store.GetSession(ws + "/live-0"); err != nil {
		t.Fatalf("expected live-0 to survive ReapDead: %v", err)
	}
}

// TestReapDeadClearsStaleHealthReading: a session marked Crashed must not
// keep publishing the health reading it carried while its pane was alive.
// After an external kill, `marvel get sessions` reported state=crashed
// alongside health=healthy: the pane's absence is the process-alive
// verdict, and ReapDead was recording the state transition without it.
// See aae-orc-4bz2.
func TestReapDeadClearsStaleHealthReading(t *testing.T) {
	skipIfNoTmux(t)

	cases := []struct {
		name  string
		prior api.HealthState
	}{
		{name: "healthy", prior: api.HealthHealthy},
		{name: "unknown", prior: api.HealthUnknown},
		{name: "already unhealthy", prior: api.HealthUnhealthy},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := api.NewStore()
			driver, err := tmux.NewDriver()
			if err != nil {
				t.Fatalf("new driver: %v", err)
			}
			mgr := NewManager(store, driver)

			ws := "test-reap-health-" + tc.name
			t.Cleanup(func() {
				_ = mgr.CleanupWorkspace(ws)
			})

			sess := &api.Session{
				Name:      "dying-0",
				Workspace: ws,
				Team:      "agents",
				Role:      "worker",
				Runtime:   api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
			}
			if err := mgr.Create(sess); err != nil {
				t.Fatalf("create session: %v", err)
			}
			if err := store.UpdateSession(sess.Key(), func(live *api.Session) error {
				live.HealthState = tc.prior
				return nil
			}); err != nil {
				t.Fatalf("seed health state: %v", err)
			}

			if err := driver.KillPane(sess.PaneID); err != nil {
				t.Fatalf("kill pane %s: %v", sess.PaneID, err)
			}
			deadline := time.Now().Add(2 * time.Second)
			for driver.HasPane(sess.PaneID) && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if driver.HasPane(sess.PaneID) {
				t.Fatalf("tmux still reports pane %s alive after kill-pane", sess.PaneID)
			}

			if reaped := mgr.ReapDead(); len(reaped) != 1 {
				t.Fatalf("expected 1 reaped session, got %d", len(reaped))
			}
			got, err := store.GetSession(sess.Key())
			if err != nil {
				t.Fatalf("get crashed marker: %v", err)
			}
			if got.State != api.SessionCrashed {
				t.Fatalf("expected state=%s, got %s", api.SessionCrashed, got.State)
			}
			if got.HealthState != api.HealthUnhealthy {
				t.Errorf("expected health=%s on a crashed session, got %s", api.HealthUnhealthy, got.HealthState)
			}
		})
	}
}

// TestReapDeadCapsCrashedMarkers verifies the store keeps at most one
// Crashed session per role — a saturated role's many crashes must not
// accumulate ghosts. See ArcavenAE/marvel#10, aae-orc-8ci.
func TestReapDeadCapsCrashedMarkers(t *testing.T) {
	skipIfNoTmux(t)

	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	mgr := NewManager(store, driver)

	ws := "test-reap-cap"
	t.Cleanup(func() {
		_ = mgr.CleanupWorkspace(ws)
	})

	// Seed a pre-existing Crashed marker for the same role.
	stale := &api.Session{
		Name:      "agents-worker-g1-0",
		Workspace: ws,
		Team:      "agents",
		Role:      "worker",
		State:     api.SessionCrashed,
		Runtime:   api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
	}
	if err := store.CreateSession(stale); err != nil {
		t.Fatalf("seed stale crashed: %v", err)
	}

	// Live session whose pane we'll kill out-of-band.
	fresh := &api.Session{
		Name:      "agents-worker-g1-1",
		Workspace: ws,
		Team:      "agents",
		Role:      "worker",
		Runtime:   api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
	}
	if err := mgr.Create(fresh); err != nil {
		t.Fatalf("create fresh: %v", err)
	}
	if err := driver.KillPane(fresh.PaneID); err != nil {
		t.Fatalf("kill pane %s: %v", fresh.PaneID, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for driver.HasPane(fresh.PaneID) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}

	reaped := mgr.ReapDead()
	if len(reaped) != 1 || reaped[0].Key != fresh.Key() {
		t.Fatalf("expected ReapDead to return only %s, got %v", fresh.Key(), reaped)
	}

	// Stale marker must be gone; fresh now Crashed.
	if _, err := store.GetSession(stale.Key()); err == nil {
		t.Fatal("expected stale crashed marker to be cleared")
	}
	got, err := store.GetSession(fresh.Key())
	if err != nil {
		t.Fatalf("fresh crashed marker missing: %v", err)
	}
	if got.State != api.SessionCrashed {
		t.Fatalf("fresh state=%s, want %s", got.State, api.SessionCrashed)
	}
}

// The bug this guards is #129: a healthy daemon on its own fleet
// reported one reap candidate, and `reap --confirm` destroyed it. The
// candidate was the base shell pane tmux creates with every session,
// which is not in the store because it is not a marvel session. Every
// pane in a healthy workspace is either recorded or not marvel's, so
// the correct answer is nothing at all.
//
// finding-012 recorded the old behavior as reap working, because it
// asserted "one candidate listed, one destroyed" rather than "nothing
// listed on a healthy fleet". This test makes the assertion that
// finding should have made.
func TestReapReportsNothingOnAHealthyFleet(t *testing.T) {
	skipIfNoTmux(t)

	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	mgr := NewManager(store, driver)

	ws := "test-reap-healthy"
	if err := store.CreateWorkspace(&api.Workspace{Name: ws}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	sess := &api.Session{
		Name:      "t-r-g1-0",
		Workspace: ws,
		Team:      "t",
		Role:      "r",
		Runtime:   api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
	}
	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })

	// Precondition: the base pane really is there and really is
	// unrecorded, or this test would pass for the wrong reason.
	panes, err := driver.ListPanes("marvel-" + ws)
	if err != nil {
		t.Fatalf("list panes: %v", err)
	}
	var base, created int
	for _, p := range panes {
		if p.Created {
			created++
		} else {
			base++
		}
	}
	if base == 0 {
		t.Fatalf("no unmarked base pane present, so this test cannot detect #129 (panes=%d)", len(panes))
	}
	if created == 0 {
		t.Fatal("no marvel-created pane is marked; the marker is not being set at all")
	}

	found, err := mgr.UnrecordedTmuxState()
	if err != nil {
		t.Fatalf("UnrecordedTmuxState: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("healthy fleet reports %d reap candidate(s), want 0: %v", len(found), found)
	}
}

// A pane marvel did not create is never a candidate, even when it sits
// inside a marvel workspace and is not in the store. An operator who
// opens a shell in a marvel session must not lose it to reap.
func TestUnrecordedTmuxStateIgnoresPanesMarvelDidNotCreate(t *testing.T) {
	skipIfNoTmux(t)

	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	mgr := NewManager(store, driver)

	ws := "test-foreign-pane"
	if err := store.CreateWorkspace(&api.Workspace{Name: ws}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	tmuxSess := "marvel-" + ws
	if err := driver.NewSession(tmuxSess); err != nil {
		t.Fatalf("new session: %v", err)
	}
	t.Cleanup(func() { _ = driver.KillSession(tmuxSess) })

	found, err := mgr.UnrecordedTmuxState()
	if err != nil {
		t.Fatalf("UnrecordedTmuxState: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("reported %d candidate(s) for a workspace whose only pane is tmux's own: %v",
			len(found), found)
	}
}

// The preview being right is not enough: --reclaim runs the kill policy
// directly. On a healthy fleet it must adopt its own pane and leave the
// base shell pane alone, or `marvel daemon --reclaim` damages the very
// fleet it is reclaiming for.
func TestAdoptOrKillSparesPanesMarvelDidNotCreate(t *testing.T) {
	skipIfNoTmux(t)

	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	mgr := NewManager(store, driver)

	ws := "test-reclaim-spares"
	if err := store.CreateWorkspace(&api.Workspace{Name: ws}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	sess := &api.Session{
		Name:      "t-r-g1-0",
		Workspace: ws,
		Team:      "t",
		Role:      "r",
		Runtime:   api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
	}
	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })

	before, err := driver.ListPanes("marvel-" + ws)
	if err != nil {
		t.Fatalf("list panes: %v", err)
	}

	adopted, killed, err := mgr.AdoptOrKill()
	if err != nil {
		t.Fatalf("AdoptOrKill: %v", err)
	}
	if adopted < 1 {
		t.Errorf("adopted = %d, want at least 1 (our own recorded pane)", adopted)
	}
	if killed != 0 {
		t.Errorf("killed = %d on a healthy fleet, want 0", killed)
	}

	after, err := driver.ListPanes("marvel-" + ws)
	if err != nil {
		t.Fatalf("list panes after: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("panes %d -> %d: the kill policy destroyed part of a healthy fleet",
			len(before), len(after))
	}
}

func TestDaemonTempDirsAreLayoutScoped(t *testing.T) {
	// No t.Parallel: t.Setenv mutates process-wide state.
	alphaHome := t.TempDir()
	betaHome := t.TempDir()

	t.Setenv("HOME", alphaHome)
	alphaProjection, alphaStream := defaultProjectionDir(), defaultStreamDir()

	t.Setenv("HOME", betaHome)
	if got := defaultProjectionDir(); got == alphaProjection {
		t.Errorf("two HOMEs share projection dir %s; concurrent daemons must not", got)
	}
	if got := defaultStreamDir(); got == alphaStream {
		t.Errorf("two HOMEs share stream dir %s; concurrent daemons must not", got)
	}

	// Back to the first HOME: a restarted daemon must land on the same
	// paths, or re-projection writes files the adopted agents never read.
	t.Setenv("HOME", alphaHome)
	if got := defaultProjectionDir(); got != alphaProjection {
		t.Errorf("projection dir = %s after returning to the same HOME, want %s", got, alphaProjection)
	}
	if got := defaultStreamDir(); got != alphaStream {
		t.Errorf("stream dir = %s after returning to the same HOME, want %s", got, alphaStream)
	}

	// The two kinds stay distinct so pipes and settings files do not mix.
	if alphaProjection == alphaStream {
		t.Errorf("projection and stream dirs are both %s, want distinct", alphaProjection)
	}
}
