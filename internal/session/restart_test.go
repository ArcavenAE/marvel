package session

import (
	"path/filepath"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/tmux"
)

// TestDaemonRestart_InMemoryKillsAll documents the degenerate behavior
// of AdoptOrKill when L2 (bbolt) is not in use: an empty store means
// no workspaces are recorded, so every marvel-* tmux session is
// classified as unrecorded and killed. This preserves the pre-L2
// contract C12 (clean-slate-on-restart, see orc finding-048) as the
// safety net for the no-persistence path.
//
// Renamed and reframed from TestDaemonRestart_DocumentsCleanSlate
// (Session 1 of aae-orc-k4e4). The previous name documented this as
// THE daemon-restart behavior; with L2 it is now one of two paths
// the same code follows depending on whether intent was recorded.
// See TestDaemonRestart_AdoptsRecordedIntent for the with-L2 path.
//
// Refs: orc finding-050, finding-048 C12, aae-orc-72u.
func TestDaemonRestart_InMemoryKillsAll(t *testing.T) {
	skipIfNoTmux(t)

	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}

	ws := "test-restart-in-memory"
	tmuxSess := "marvel-" + ws

	// Phase 1: "old daemon" with in-memory-only store spawns two sessions.
	oldStore := api.NewStore()
	oldMgr := NewManager(oldStore, driver)

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

	panes, err := driver.ListPanes(tmuxSess)
	if err != nil {
		t.Fatalf("list panes after old daemon: %v", err)
	}
	if len(panes) < 2 {
		t.Fatalf("old daemon: expected at least 2 panes, got %d", len(panes))
	}

	// Phase 2: simulate daemon death. Drop refs; in-memory state is
	// gone but tmux panes persist (tmux is the parent).
	oldMgr = nil
	oldStore = nil
	_ = oldMgr
	_ = oldStore

	if !driver.HasSession(tmuxSess) {
		t.Fatalf("precondition: tmux session %s missing after daemon death", tmuxSess)
	}

	// Phase 3: "new daemon" with fresh in-memory-only store. With no
	// recorded intent, AdoptOrKill kills everything marvel-*.
	newStore := api.NewStore()
	newMgr := NewManager(newStore, driver)

	adopted, killed, err := newMgr.AdoptOrKill()
	if err != nil {
		t.Fatalf("AdoptOrKill: %v", err)
	}
	if adopted != 0 {
		t.Fatalf("in-memory mode: expected 0 adopted, got %d", adopted)
	}
	if killed == 0 {
		t.Fatalf("in-memory mode: expected sessions to be killed, got %d", killed)
	}

	if driver.HasSession(tmuxSess) {
		t.Fatalf("after AdoptOrKill (in-memory mode), tmux session %s still exists — C12 fallback is broken", tmuxSess)
	}
	if sessions := newStore.ListSessions(); len(sessions) != 0 {
		t.Fatalf("after AdoptOrKill, store should be empty: got %d sessions", len(sessions))
	}
}

// TestDaemonRestart_AdoptsRecordedIntent verifies the architectural
// pivot: with L2 (bbolt) in use, a restarted daemon adopts panes
// matching recorded intent rather than killing them. Self-update
// without trauma (orc question-marvel-transaction-log Mr-Right-Now)
// rests on this property.
//
// Session 2 of aae-orc-k4e4 landing this test is the explicit
// reversal of contract C12 (orc finding-048).
func TestDaemonRestart_AdoptsRecordedIntent(t *testing.T) {
	skipIfNoTmux(t)

	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}

	boltPath := filepath.Join(t.TempDir(), "marvel.bolt")
	ws := "test-adopt-intent"
	tmuxSess := "marvel-" + ws

	t.Cleanup(func() {
		_ = driver.KillSession(tmuxSess)
	})

	// Phase 1: "old daemon" — bbolt open, workspace recorded, sessions
	// spawned. Each spawn writes the session (with PaneID) to bbolt.
	oldStore := api.NewStore()
	if err := oldStore.OpenBolt(boltPath); err != nil {
		t.Fatalf("OpenBolt (old daemon): %v", err)
	}
	if err := oldStore.CreateWorkspace(&api.Workspace{Name: ws}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	oldMgr := NewManager(oldStore, driver)
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

	// Snapshot expected pane IDs so we can verify they're preserved.
	oldSessions := oldStore.ListSessions()
	if len(oldSessions) != 2 {
		t.Fatalf("setup: want 2 sessions, got %d", len(oldSessions))
	}
	expectedPanes := make(map[string]bool, len(oldSessions))
	for _, s := range oldSessions {
		if s.PaneID == "" {
			t.Fatalf("setup: session %s has no PaneID", s.Key())
		}
		expectedPanes[s.PaneID] = true
	}

	// Phase 2: simulate daemon death — flush + close bolt, drop refs.
	// The bbolt file persists on disk; tmux processes persist outside
	// our process.
	if err := oldStore.CloseBolt(); err != nil {
		t.Fatalf("CloseBolt: %v", err)
	}
	oldMgr = nil
	oldStore = nil
	_ = oldMgr
	_ = oldStore

	if !driver.HasSession(tmuxSess) {
		t.Fatalf("precondition: tmux session %s missing after daemon death", tmuxSess)
	}

	// Phase 3: "new daemon" — fresh store, open bolt (rehydrate from disk),
	// AdoptOrKill should preserve the recorded panes.
	newStore := api.NewStore()
	if err := newStore.OpenBolt(boltPath); err != nil {
		t.Fatalf("OpenBolt (new daemon, rehydrate): %v", err)
	}
	t.Cleanup(func() { _ = newStore.CloseBolt() })

	// Sanity: rehydrate brought back the workspace + sessions.
	if _, err := newStore.GetWorkspace(ws); err != nil {
		t.Fatalf("rehydrate missing workspace: %v", err)
	}
	rehydratedSessions := newStore.ListSessions()
	if len(rehydratedSessions) != 2 {
		t.Fatalf("rehydrate: want 2 sessions, got %d", len(rehydratedSessions))
	}

	newMgr := NewManager(newStore, driver)
	adopted, _, err := newMgr.AdoptOrKill()
	if err != nil {
		t.Fatalf("AdoptOrKill: %v", err)
	}

	// Architectural pivot: agents are adopted, not killed.
	if adopted != 2 {
		t.Fatalf("want 2 adopted (the recorded agents), got %d", adopted)
	}

	// (Note: AdoptOrKill MAY kill the default shell pane that tmux
	// new-session created on Phase 1, which is unrecorded. That's
	// consistent with the pre-L2 behavior which destroyed the entire
	// session including the default shell — see TestDaemonRestart_
	// InMemoryKillsAll. We don't assert on killed count because the
	// default-shell-pane existence is a tmux implementation detail.)

	// tmux session survives — at least 2 panes adopted means the
	// session is alive.
	if !driver.HasSession(tmuxSess) {
		t.Fatalf("after AdoptOrKill, tmux session %s gone — pivot is broken", tmuxSess)
	}

	// Every recorded session's pane is still alive after AdoptOrKill.
	for _, s := range rehydratedSessions {
		if !driver.HasPane(s.PaneID) {
			t.Fatalf("after AdoptOrKill, recorded pane %s (session %s) is gone — adoption failed",
				s.PaneID, s.Key())
		}
	}
}

// TestDaemonRestart_KillsUnrecordedPanes verifies the other half of
// AdoptOrKill: panes that don't match recorded intent are killed.
// This is the safety property — adopt only what was intended, not
// every pane that happens to be in a marvel-* tmux session.
func TestDaemonRestart_KillsUnrecordedPanes(t *testing.T) {
	skipIfNoTmux(t)

	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}

	boltPath := filepath.Join(t.TempDir(), "marvel.bolt")
	ws := "test-kill-unrecorded"
	tmuxSess := "marvel-" + ws

	t.Cleanup(func() {
		_ = driver.KillSession(tmuxSess)
	})

	// Phase 1: spawn ONE session via Manager (records to bbolt with
	// PaneID), then create an EXTRA pane via direct driver call so
	// it has no bbolt record.
	s1 := api.NewStore()
	if err := s1.OpenBolt(boltPath); err != nil {
		t.Fatalf("OpenBolt: %v", err)
	}
	if err := s1.CreateWorkspace(&api.Workspace{Name: ws}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	m1 := NewManager(s1, driver)
	sess := &api.Session{
		Name:      "agent-0",
		Workspace: ws,
		Team:      "team",
		Role:      "worker",
		Runtime:   api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
	}
	if err := m1.Create(sess); err != nil {
		t.Fatalf("Create recorded session: %v", err)
	}
	recordedPaneID := sess.PaneID

	// Inject an unrecorded pane directly via the driver. No bbolt
	// record exists for this pane.
	unrecordedPaneID, err := driver.NewPane(tmuxSess, "sleep 300", "unrecorded", nil)
	if err != nil {
		t.Fatalf("inject unrecorded pane: %v", err)
	}

	// Phase 2: simulate daemon death.
	if err := s1.CloseBolt(); err != nil {
		t.Fatalf("CloseBolt: %v", err)
	}

	// Phase 3: new daemon rehydrates, AdoptOrKill.
	s2 := api.NewStore()
	if err := s2.OpenBolt(boltPath); err != nil {
		t.Fatalf("OpenBolt (new daemon): %v", err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })

	m2 := NewManager(s2, driver)
	adopted, killed, err := m2.AdoptOrKill()
	if err != nil {
		t.Fatalf("AdoptOrKill: %v", err)
	}

	if adopted != 1 {
		t.Fatalf("want 1 adopted, got %d", adopted)
	}
	// At least 1 killed (the explicit unrecorded pane). The default
	// shell pane from tmux new-session may also be killed, so
	// killed >= 1.
	if killed < 1 {
		t.Fatalf("want at least 1 killed (the unrecorded pane), got %d", killed)
	}

	if driver.HasPane(unrecordedPaneID) {
		t.Fatalf("unrecorded pane %s still alive after AdoptOrKill", unrecordedPaneID)
	}
	if !driver.HasPane(recordedPaneID) {
		t.Fatalf("recorded pane %s killed during AdoptOrKill — should have been adopted", recordedPaneID)
	}
}
