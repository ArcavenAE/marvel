package api

import (
	"fmt"
	"testing"
)

func seedSessions(t *testing.T, s *Store, n int) []string {
	t.Helper()
	keys := make([]string, 0, n)
	for i := 0; i < n; i++ {
		sess := &Session{Name: fmt.Sprintf("a-%d", i), Workspace: "ws", Team: "t", Role: "r", State: SessionRunning, PaneID: fmt.Sprintf("%%%d", i)}
		if err := s.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, sess.Key())
	}
	return keys
}

// A tick that changes N records starts one transaction and advances the
// version once; the same tick through Store.UpdateSession starts N.
func TestWriteSetFlushesAChangedTickInOneTransaction(t *testing.T) {
	t.Parallel()
	s, path := openCountedStore(t)
	keys := seedSessions(t, s, 12)
	u0, v0 := s.BoltUpdates(), s.ResourceVersion()

	ws := s.NewWriteSet()
	for _, k := range keys {
		if err := ws.UpdateSession(k, func(live *Session) error {
			live.PaneID += "x"
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Memory is current before Flush: a read later in the tick sees the update.
	got, err := s.GetSession(keys[3])
	if err != nil {
		t.Fatal(err)
	}
	if got.PaneID != "%3x" {
		t.Errorf("before Flush the live PaneID = %q, want %%3x", got.PaneID)
	}
	if n := s.BoltUpdates() - u0; n != 0 {
		t.Errorf("updates before Flush started %d transactions, want 0", n)
	}
	if err := ws.Flush(); err != nil {
		t.Fatal(err)
	}
	if n := s.BoltUpdates() - u0; n != 1 {
		t.Errorf("12 changed records started %d transactions, want 1", n)
	}
	if n := s.ResourceVersion() - v0; n != 1 {
		t.Errorf("12 changed records advanced the version by %d, want 1", n)
	}

	// Every record is on disk.
	if err := s.CloseBolt(); err != nil {
		t.Fatal(err)
	}
	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	for i, k := range keys {
		g, err := s2.GetSession(k)
		if err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("%%%dx", i); g.PaneID != want {
			t.Errorf("%s PaneID after reopen = %q, want %q", k, g.PaneID, want)
		}
	}
}

func TestWriteSetFlushOfAnUnchangedTickStartsNoTransaction(t *testing.T) {
	t.Parallel()
	s, _ := openCountedStore(t)
	keys := seedSessions(t, s, 6)
	u0, v0 := s.BoltUpdates(), s.ResourceVersion()

	ws := s.NewWriteSet()
	for _, k := range keys {
		if err := ws.UpdateSession(k, func(*Session) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if err := ws.Flush(); err != nil {
		t.Fatal(err)
	}
	if n := s.BoltUpdates() - u0; n != 0 {
		t.Errorf("an unchanged tick started %d transactions, want 0", n)
	}
	if n := s.ResourceVersion() - v0; n != 0 {
		t.Errorf("an unchanged tick advanced the version by %d, want 0", n)
	}
}

// Only the records that changed are in the transaction, and a record updated
// twice in one tick is written once.
func TestWriteSetWritesOnlyWhatChanged(t *testing.T) {
	t.Parallel()
	s, path := openCountedStore(t)
	keys := seedSessions(t, s, 4)
	u0 := s.BoltUpdates()

	ws := s.NewWriteSet()
	for i := 0; i < 2; i++ {
		if err := ws.UpdateSession(keys[1], func(live *Session) error {
			live.PaneID = fmt.Sprintf("%%1-%d", i)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := ws.UpdateSession(keys[2], func(*Session) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := ws.Flush(); err != nil {
		t.Fatal(err)
	}
	if n := s.BoltUpdates() - u0; n != 1 {
		t.Errorf("started %d transactions, want 1", n)
	}
	if err := s.CloseBolt(); err != nil {
		t.Fatal(err)
	}
	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	if g, err := s2.GetSession(keys[1]); err != nil || g.PaneID != "%1-1" {
		t.Errorf("the changed record after reopen = %+v, %v; want PaneID %%1-1", g, err)
	}
}

// A record deleted between its update and the Flush is skipped, and one a
// direct update already wrote is not written again.
func TestWriteSetSkipsADeletedRecordAndOneAlreadyWritten(t *testing.T) {
	t.Parallel()
	s, _ := openCountedStore(t)
	keys := seedSessions(t, s, 3)

	ws := s.NewWriteSet()
	for _, k := range keys {
		if err := ws.UpdateSession(k, func(live *Session) error {
			live.PaneID += "y"
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteSession(keys[0]); err != nil {
		t.Fatal(err)
	}
	// A direct update writes keys[1] with the set's change already in memory.
	if err := s.UpdateSession(keys[1], func(*Session) error { return nil }); err != nil {
		t.Fatal(err)
	}
	u0 := s.BoltUpdates()
	if err := ws.Flush(); err != nil {
		t.Fatal(err)
	}
	if n := s.BoltUpdates() - u0; n != 1 {
		t.Errorf("Flush started %d transactions, want 1 (only keys[2] is left to write)", n)
	}
}

func TestWriteSetFlushReportsOneErrorForTheTick(t *testing.T) {
	t.Parallel()
	s, _ := openCountedStore(t)
	keys := seedSessions(t, s, 5)
	ws := s.NewWriteSet()
	for _, k := range keys {
		if err := ws.UpdateSession(k, func(live *Session) error {
			live.PaneID += "z"
			return nil
		}); err != nil {
			t.Fatalf("an update returned the persist error early: %v", err)
		}
	}
	// Close the database handle under the Store, so the write fails.
	if err := s.bolt.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ws.Flush(); err == nil {
		t.Fatal("Flush returned nil with the database closed")
	}
	// Memory is updated regardless.
	if g, err := s.GetSession(keys[0]); err != nil || g.PaneID != "%0z" {
		t.Errorf("live record after a failed Flush = %+v, %v; want PaneID %%0z", g, err)
	}
	if err := ws.Flush(); err != nil {
		t.Errorf("a second Flush with nothing queued returned %v, want nil", err)
	}
}

func TestWriteSetUpdateTeamOneTransaction(t *testing.T) {
	t.Parallel()
	s, _ := openCountedStore(t)
	for i := 0; i < 3; i++ {
		if err := s.CreateTeam(&Team{Name: fmt.Sprintf("t%d", i), Workspace: "ws", Generation: 1}); err != nil {
			t.Fatal(err)
		}
	}
	u0 := s.BoltUpdates()
	ws := s.NewWriteSet()
	for i := 0; i < 3; i++ {
		if err := ws.UpdateTeam(fmt.Sprintf("ws/t%d", i), func(live *Team) error {
			live.Generation = 5
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := ws.Flush(); err != nil {
		t.Fatal(err)
	}
	if n := s.BoltUpdates() - u0; n != 1 {
		t.Errorf("3 changed teams started %d transactions, want 1", n)
	}
}

// A record the set queued and a direct update then wrote leaves nothing for
// Flush to do: no transaction, not a second write of the same bytes.
func TestWriteSetFlushSkipsARecordAlreadyWritten(t *testing.T) {
	t.Parallel()
	s, _ := openCountedStore(t)
	keys := seedSessions(t, s, 2)
	ws := s.NewWriteSet()
	if err := ws.UpdateSession(keys[0], func(live *Session) error {
		live.PaneID = "%0w"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSession(keys[0], func(*Session) error { return nil }); err != nil {
		t.Fatal(err)
	}
	u0, v0 := s.BoltUpdates(), s.ResourceVersion()
	if err := ws.Flush(); err != nil {
		t.Fatal(err)
	}
	if n := s.BoltUpdates() - u0; n != 0 {
		t.Errorf("Flush of a record already written started %d transactions, want 0", n)
	}
	if n := s.ResourceVersion() - v0; n != 0 {
		t.Errorf("Flush of a record already written advanced the version by %d, want 0", n)
	}
}
