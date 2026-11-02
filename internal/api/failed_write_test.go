package api

import (
	"strings"
	"testing"
)

// A failed write is not remembered as written, so the next tick tries again
// (review of marvel#1228). bbolt refuses a key over 32768 bytes, which gives a
// write that fails and then can succeed: the session's stored key is its
// workspace and name, so a callback that gives it a long name makes the
// transaction fail, and a later one that puts the name back makes it work.

func overlongName() string { return strings.Repeat("n", 40000) }

func TestFailedUpdateIsNotRememberedAndTheNextUpdateRetries(t *testing.T) {
	t.Parallel()
	s, _ := openCountedStore(t)
	sess := &Session{Name: "a-0", Workspace: "ws", Team: "t", Role: "r", State: SessionRunning, PaneID: "%1"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	key := sess.Key()
	u0 := s.BoltUpdates()

	err := s.UpdateSession(key, func(live *Session) error {
		live.Name = overlongName()
		return nil
	})
	if err == nil {
		t.Fatal("an update whose stored key is over bbolt's limit returned nil")
	}
	if n := s.BoltUpdates() - u0; n != 1 {
		t.Fatalf("the failing update started %d transactions, want 1", n)
	}
	if _, ok := s.persisted[persistedKey(bucketSessions, "ws/"+overlongName())]; ok {
		t.Error("the failed write was remembered as written")
	}

	// Nothing about the record changes now, and the file still lacks it: the
	// update tries the write again, and fails again.
	if err := s.UpdateSession(key, func(*Session) error { return nil }); err == nil {
		t.Error("the update after a failed write returned nil: the failed bytes were taken as already written")
	}
	if n := s.BoltUpdates() - u0; n != 2 {
		t.Errorf("two updates started %d transactions, want 2 (the second a retry)", n)
	}
}

// The same for Flush. A write set queues a record under its own key, so this
// failure comes from closing the database handle under the Store instead: every
// attempt fails, and each one is counted, which is enough to tell a retry from a
// skip.
func TestFailedFlushIsNotRememberedAndTheNextFlushRetries(t *testing.T) {
	t.Parallel()
	s, _ := openCountedStore(t)
	sess := &Session{Name: "a-0", Workspace: "ws", Team: "t", Role: "r", State: SessionRunning, PaneID: "%1"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	key := sess.Key()

	ws := s.NewWriteSet()
	if err := ws.UpdateSession(key, func(live *Session) error {
		live.PaneID = "%9"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.bolt.Close(); err != nil {
		t.Fatal(err)
	}
	u0 := s.BoltUpdates()
	if err := ws.Flush(); err == nil {
		t.Fatal("a Flush with the database closed returned nil")
	}
	if n := s.BoltUpdates() - u0; n != 1 {
		t.Fatalf("the failing Flush started %d transactions, want 1", n)
	}

	// The next tick touches the record without changing it. The file still
	// lacks the new PaneID, so that Flush tries again, and fails again.
	ws2 := s.NewWriteSet()
	if err := ws2.UpdateSession(key, func(*Session) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := ws2.Flush(); err == nil {
		t.Error("the Flush after a failed one returned nil: the failed bytes were taken as already written")
	}
	if n := s.BoltUpdates() - u0; n != 2 {
		t.Errorf("two Flushes started %d transactions, want 2 (the second a retry)", n)
	}
}
