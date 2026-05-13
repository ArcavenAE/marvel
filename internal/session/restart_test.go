package session

import (
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/tmux"
)

// TestDaemonRestart_DocumentsCleanSlate pins the current daemon-restart
// behavior: CleanupOrphanTmux kills every marvel-* tmux session,
// intentionally destroying any panes from a previous daemon instance.
// This is the fix from ArcavenAE/marvel#13 (aae-orc-72u) — clean-slate
// kill chosen over reconnect-and-reconcile.
//
// Documents contract C12 from orc finding-048-marvel-tmux-contracts.md:
// marvel cannot survive its own restart while keeping agents alive — by
// design, because no durable record of intent (L2) exists for the new
// daemon to reconcile back to. Failure modes FM1/FM2/FM3 (daemon SIGKILL
// / graceful stop / self-update) all hinge on this contract.
//
// When marvel/_kos/nodes/frontier/question-marvel-transaction-log
// crystallizes and CleanupOrphanTmux is reworked to adopt-or-kill
// against recorded intent, this test should flip to assert "panes that
// match recorded intent are adopted, others are killed." That flip is
// the architectural pivot — this test pins the before-state so the
// after-state has something to be compared against.
//
// Refs: orc finding-048-marvel-tmux-contracts.md, aae-orc-72u (closed),
// aae-orc-dwnm (audit), question-marvel-transaction-log (future fix).
func TestDaemonRestart_DocumentsCleanSlate(t *testing.T) {
	skipIfNoTmux(t)

	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}

	ws := "test-restart-clean-slate"
	tmuxSess := "marvel-" + ws

	// Phase 1: simulate "old daemon" — spawn two sessions.
	// The Manager+Store represents the daemon's in-memory state.
	oldStore := api.NewStore()
	oldMgr := NewManager(oldStore, driver)

	// Belt-and-suspenders cleanup: even if assertions fail mid-test,
	// don't leave panes running. The CleanupOrphanTmux call below
	// should handle it, but t.Cleanup is the safety net.
	t.Cleanup(func() {
		_ = driver.KillSession(tmuxSess)
	})

	for _, name := range []string{"agent-0", "agent-1"} {
		sess := &api.Session{
			Name:      name,
			Workspace: ws,
			Team:      "team",
			Role:      "worker",
			Runtime:   api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
		}
		if err := oldMgr.Create(sess); err != nil {
			t.Fatalf("old daemon create %s: %v", name, err)
		}
	}

	// Verify the old daemon's panes exist at tmux level — the precondition
	// for the test (panes survive the daemon's death because tmux is the
	// parent, finding-048 C10).
	//
	// tmux new-session creates an initial default-shell pane on top of
	// the two agent windows marvel adds — so the count is "at least the
	// two agents," not exactly two. The post-cleanup check is the real
	// assertion; this is just a sanity check that Phase 1 ran.
	panes, err := driver.ListPanes(tmuxSess)
	if err != nil {
		t.Fatalf("list panes after old daemon: %v", err)
	}
	if len(panes) < 2 {
		t.Fatalf("old daemon: expected at least 2 panes (agent-0 and agent-1), got %d", len(panes))
	}

	// Phase 2: simulate daemon death. Drop the old Manager+Store entirely
	// — the marvel daemon process is gone. The tmux session and panes
	// persist (tmux server is a separate process, see C12 commentary in
	// finding-048).
	//
	// We deliberately do NOT call mgr.Delete or KillSession here — the
	// old daemon died ungracefully (SIGKILL / OOM / panic), it had no
	// chance to drain.
	oldMgr = nil
	oldStore = nil
	_ = oldMgr   // silence unused
	_ = oldStore // silence unused

	// Confirm the precondition: panes are still alive at the tmux level
	// after the "daemon" died.
	if !driver.HasSession(tmuxSess) {
		t.Fatalf("precondition failed: tmux session %s missing before new daemon starts (should survive daemon death)", tmuxSess)
	}

	// Phase 3: "new daemon" starts with a fresh in-memory state and runs
	// CleanupOrphanTmux on startup, mimicking daemon.go:201.
	newStore := api.NewStore()
	newMgr := NewManager(newStore, driver)

	if err := newMgr.CleanupOrphanTmux(); err != nil {
		t.Fatalf("new daemon CleanupOrphanTmux: %v", err)
	}

	// Assert contract C12: the panes from the old daemon are GONE.
	// Marvel's response to orphans is to destroy them, not adopt them.
	//
	// When question-marvel-transaction-log lands, this assertion flips:
	// recorded-intent sessions should be ADOPTED (HasSession true,
	// store populated). Whichever way it flips, the assertion must be
	// explicit — silent behavior changes here would be invisible to
	// callers, which is the entire reason this test exists.
	if driver.HasSession(tmuxSess) {
		t.Fatalf("after CleanupOrphanTmux, tmux session %s still exists — clean-slate (C12) is broken", tmuxSess)
	}

	// Assert: the new daemon's store stays empty. No adoption, no
	// reconstruction-from-tmux-state. The new daemon would reconcile
	// from manifest, which this test doesn't load — so the store
	// SHOULD be empty.
	sessions := newStore.ListSessions()
	if len(sessions) != 0 {
		t.Fatalf("after CleanupOrphanTmux, new store has %d sessions — adoption shouldn't happen with no L2 in place", len(sessions))
	}
}
