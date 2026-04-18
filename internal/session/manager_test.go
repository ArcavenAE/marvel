package session

import (
	"os/exec"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
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

func TestCleanupOrphanTmux(t *testing.T) {
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

	mgr := NewManager(api.NewStore(), driver)
	if err := mgr.cleanupOrphanTmuxPrefix(prefix); err != nil {
		t.Fatalf("cleanup orphan tmux: %v", err)
	}

	for _, name := range orphans {
		if driver.HasSession(name) {
			t.Fatalf("orphan %s should have been killed", name)
		}
	}
	if !driver.HasSession(outsider) {
		t.Fatalf("non-prefix session %s must not be touched", outsider)
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
