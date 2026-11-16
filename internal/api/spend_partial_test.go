package api

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func partialSpendSession(t *testing.T, s *Store) string {
	t.Helper()
	sess := &Session{Name: "a", Workspace: "ws", Team: "t", Role: "r", State: SessionRunning, PaneID: "%1"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	return sess.Key()
}

// The spend write carries the lower-bound flag with the sum (I-477 slice 1).
func TestSpendUpdateStoresThePartialFlag(t *testing.T) {
	t.Parallel()
	s := NewStore()
	key := partialSpendSession(t, s)
	out := 200
	s.UpdateSessionSpend(key, SessionSpend{Out: &out, Partial: true})
	got, _ := s.GetSession(key)
	if !got.SpendPartial {
		t.Error("SpendPartial = false after a spend write that said partial")
	}
	s.UpdateSessionSpend(key, SessionSpend{Out: &out, Partial: false})
	got, _ = s.GetSession(key)
	if got.SpendPartial {
		t.Error("SpendPartial stayed true after a spend write that said exact")
	}
}

// A heartbeat replaces the occupancy and must leave the spend slice, the flag
// included, as the accountant or reader wrote it (marvel#779).
func TestHeartbeatKeepsThePartialFlag(t *testing.T) {
	t.Parallel()
	s := NewStore()
	key := partialSpendSession(t, s)
	out := 200
	s.UpdateSessionSpend(key, SessionSpend{Out: &out, Partial: true})
	if _, err := s.UpdateSessionHeartbeat(HeartbeatRequest{SessionKey: key, ContextPercent: 40}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetSession(key)
	if got.SpendOut == nil || *got.SpendOut != 200 {
		t.Errorf("a heartbeat changed SpendOut to %v", got.SpendOut)
	}
	if !got.SpendPartial {
		t.Error("a heartbeat cleared SpendPartial")
	}
}

// The flag survives a daemon restart with the sum it qualifies.
func TestBoltLoadKeepsThePartialFlag(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	s1 := NewStore()
	if err := s1.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #1: %v", err)
	}
	key := partialSpendSession(t, s1)
	out := 200
	s1.UpdateSessionSpend(key, SessionSpend{Out: &out, Partial: true})
	if err := s1.UpdateSession(key, func(*Session) error { return nil }); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	if err := s1.CloseBolt(); err != nil {
		t.Fatalf("CloseBolt: %v", err)
	}
	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #2: %v", err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	got, err := s2.GetSession(key)
	if err != nil {
		t.Fatal(err)
	}
	if got.SpendOut == nil || *got.SpendOut != 200 {
		t.Errorf("after a restart SpendOut = %v, want 200", got.SpendOut)
	}
	if !got.SpendPartial {
		t.Error("after a restart SpendPartial = false, want true")
	}
}

// spend_partial is on the wire for every session, false included, so an old
// reader and a new one can tell "exact" from "field missing".
func TestSpendPartialIsAlwaysOnTheWire(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(Session{Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"spend_partial":false`) {
		t.Errorf("a session with no spend does not carry spend_partial:false: %s", raw)
	}
	raw, _ = json.Marshal(Session{SessionContext: SessionContext{SpendPartial: true}})
	if !strings.Contains(string(raw), `"spend_partial":true`) {
		t.Errorf("a partial sum does not carry spend_partial:true: %s", raw)
	}
}

// The reader's place in the file is stored with the sum, kept through a
// heartbeat, and survives a daemon restart, so a restarted reader can seed its
// per-id figures up to that offset.
func TestSpendTailIsStoredKeptAndReloaded(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	s1 := NewStore()
	if err := s1.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #1: %v", err)
	}
	key := partialSpendSession(t, s1)
	out := 200
	s1.UpdateSessionSpend(key, SessionSpend{Out: &out, Tail: &SpendTail{Offset: 4096, Inode: 77}})
	got, _ := s1.GetSession(key)
	if got.SpendTail == nil || got.SpendTail.Offset != 4096 || got.SpendTail.Inode != 77 {
		t.Fatalf("SpendTail after a spend write = %+v, want offset 4096 inode 77", got.SpendTail)
	}
	if _, err := s1.UpdateSessionHeartbeat(HeartbeatRequest{SessionKey: key, ContextPercent: 40}); err != nil {
		t.Fatal(err)
	}
	got, _ = s1.GetSession(key)
	if got.SpendTail == nil || got.SpendTail.Offset != 4096 {
		t.Errorf("a heartbeat changed SpendTail to %+v", got.SpendTail)
	}
	if err := s1.UpdateSession(key, func(*Session) error { return nil }); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	if err := s1.CloseBolt(); err != nil {
		t.Fatalf("CloseBolt: %v", err)
	}
	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #2: %v", err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	got, err := s2.GetSession(key)
	if err != nil {
		t.Fatal(err)
	}
	if got.SpendTail == nil || got.SpendTail.Offset != 4096 || got.SpendTail.Inode != 77 {
		t.Errorf("SpendTail after a reload = %+v, want offset 4096 inode 77", got.SpendTail)
	}
}
