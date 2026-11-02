package api

import (
	"errors"
	"path/filepath"
	"testing"
)

// marvel#1214: an update that leaves the file's copy of a record as it is
// starts no bbolt transaction. These tests count transactions with Store.BoltUpdates
// and read the version with Store.ResourceVersion, which advances once per
// transaction.

func openCountedStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	s := NewStore()
	if err := s.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt: %v", err)
	}
	t.Cleanup(func() { _ = s.CloseBolt() })
	return s, path
}

func TestUpdateSessionThatChangesNothingStartsNoTransaction(t *testing.T) {
	t.Parallel()
	s, _ := openCountedStore(t)
	sess := &Session{Name: "a-0", Workspace: "ws", Team: "t", Role: "r", State: SessionRunning, PaneID: "%1"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	u0, v0 := s.BoltUpdates(), s.ResourceVersion()

	for i := 0; i < 5; i++ {
		if err := s.UpdateSession(sess.Key(), func(live *Session) error {
			live.State = SessionRunning // the value it already holds
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.BoltUpdates() - u0; got != 0 {
		t.Errorf("5 updates that changed nothing started %d transactions, want 0", got)
	}
	if got := s.ResourceVersion() - v0; got != 0 {
		t.Errorf("5 updates that changed nothing advanced the version by %d, want 0", got)
	}
}

func TestUpdateSessionThatChangesSomethingStartsOneTransaction(t *testing.T) {
	t.Parallel()
	s, path := openCountedStore(t)
	sess := &Session{Name: "a-0", Workspace: "ws", Team: "t", Role: "r", State: SessionRunning, PaneID: "%1"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	u0, v0 := s.BoltUpdates(), s.ResourceVersion()

	if err := s.UpdateSession(sess.Key(), func(live *Session) error {
		live.PaneID = "%9"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := s.BoltUpdates() - u0; got != 1 {
		t.Errorf("one real change started %d transactions, want 1", got)
	}
	if got := s.ResourceVersion() - v0; got != 1 {
		t.Errorf("one real change advanced the version by %d, want 1", got)
	}

	// The change is durable: a second store on the same file reads it.
	if err := s.CloseBolt(); err != nil {
		t.Fatal(err)
	}
	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	got, err := s2.GetSession(sess.Key())
	if err != nil {
		t.Fatal(err)
	}
	if got.PaneID != "%9" {
		t.Errorf("PaneID after reopen = %q, want %%9", got.PaneID)
	}
}

func TestUpdateSessionThatFailsWritesNothing(t *testing.T) {
	t.Parallel()
	s, _ := openCountedStore(t)
	sess := &Session{Name: "a-0", Workspace: "ws", Team: "t", Role: "r", State: SessionRunning, PaneID: "%1"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	u0 := s.BoltUpdates()
	boom := errors.New("boom")
	err := s.UpdateSession(sess.Key(), func(live *Session) error {
		live.PaneID = "%9"
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("UpdateSession error = %v, want boom", err)
	}
	if got := s.BoltUpdates() - u0; got != 0 {
		t.Errorf("a failed update started %d transactions, want 0", got)
	}
}

func TestUpdateTeamThatChangesNothingStartsNoTransaction(t *testing.T) {
	t.Parallel()
	s, _ := openCountedStore(t)
	team := &Team{Name: "t", Workspace: "ws", Generation: 1}
	if err := s.CreateTeam(team); err != nil {
		t.Fatal(err)
	}
	u0, v0 := s.BoltUpdates(), s.ResourceVersion()
	if err := s.UpdateTeam(team.Key(), func(live *Team) error {
		live.Generation = 1
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := s.BoltUpdates() - u0; got != 0 {
		t.Errorf("a team update that changed nothing started %d transactions, want 0", got)
	}
	if got := s.ResourceVersion() - v0; got != 0 {
		t.Errorf("a team update that changed nothing advanced the version by %d, want 0", got)
	}

	if err := s.UpdateTeam(team.Key(), func(live *Team) error {
		live.Generation = 2
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := s.BoltUpdates() - u0; got != 1 {
		t.Errorf("a real team change started %d transactions, want 1", got)
	}
}

// With no bolt file the store is in-memory only and the compare is skipped;
// the update still applies.
func TestUpdateSessionWithoutBoltStillApplies(t *testing.T) {
	t.Parallel()
	s := NewStore()
	sess := &Session{Name: "a-0", Workspace: "ws", Team: "t", Role: "r", State: SessionRunning}
	if err := s.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSession(sess.Key(), func(live *Session) error {
		live.PaneID = "%4"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSession(sess.Key())
	if err != nil {
		t.Fatal(err)
	}
	if got.PaneID != "%4" {
		t.Errorf("PaneID = %q, want %%4", got.PaneID)
	}
}

// UpdateSessionContext and UpdateSessionSpend change memory only; the next
// persist carries them to the file. A no-op UpdateSession after one of them
// therefore has bytes the file lacks, and must write them once and then stop.
func TestMemoryOnlyChangeIsCarriedByTheNextUpdateOnce(t *testing.T) {
	t.Parallel()
	s, path := openCountedStore(t)
	sess := &Session{Name: "a-0", Workspace: "ws", Team: "t", Role: "r", State: SessionRunning, PaneID: "%1"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	s.UpdateSessionContext(sess.Key(), SessionContext{ContextSource: ContextSourceHeartbeat, ContextPercent: 40})
	u0 := s.BoltUpdates()

	noop := func(*Session) error { return nil }
	if err := s.UpdateSession(sess.Key(), noop); err != nil {
		t.Fatal(err)
	}
	if got := s.BoltUpdates() - u0; got != 1 {
		t.Errorf("the no-op after a memory-only change started %d transactions, want 1", got)
	}
	if err := s.UpdateSession(sess.Key(), noop); err != nil {
		t.Fatal(err)
	}
	if got := s.BoltUpdates() - u0; got != 1 {
		t.Errorf("a second no-op raised the count to %d, want it to stay 1", got)
	}

	if err := s.CloseBolt(); err != nil {
		t.Fatal(err)
	}
	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	got, err := s2.GetSession(sess.Key())
	if err != nil {
		t.Fatal(err)
	}
	if got.ContextPercent != 40 {
		t.Errorf("ContextPercent after reopen = %v, want 40", got.ContextPercent)
	}
}

// A key this process has not written yet always writes once, so a record
// loaded from an older file is never skipped on the strength of a guess.
func TestFirstUpdateAfterReopenWritesOnce(t *testing.T) {
	t.Parallel()
	s, path := openCountedStore(t)
	sess := &Session{Name: "a-0", Workspace: "ws", Team: "t", Role: "r", State: SessionRunning}
	if err := s.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseBolt(); err != nil {
		t.Fatal(err)
	}
	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	u0 := s2.BoltUpdates()
	noop := func(*Session) error { return nil }
	for i := 0; i < 3; i++ {
		if err := s2.UpdateSession(sess.Key(), noop); err != nil {
			t.Fatal(err)
		}
	}
	if got := s2.BoltUpdates() - u0; got != 1 {
		t.Errorf("three no-ops after a reopen started %d transactions, want 1", got)
	}
}
