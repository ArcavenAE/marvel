package api

import (
	"testing"
	"time"
)

// The skip-if-unchanged compare (marvel#1214) decides, for each queued or
// updated record, whether its stored form differs from the file's. It has to
// encode the record to know. A session that carries a LastHealthCheck has a
// compare image (the record without that field) that differs from the full
// record, and an unchanged session should cost one encode, not two.

func healthCheckedSession() *Session {
	return &Session{
		Name: "a-0", Workspace: "ws", Team: "t", Role: "r", State: SessionRunning, PaneID: "%1",
		LastHealthCheck: time.Date(2026, 11, 13, 5, 0, 0, 0, time.UTC),
	}
}

// encodesOf is how many record encodes the store's compare has done so far.
func encodesOf(s *Store) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.encodes
}

// The encodes are counted, not the allocations they cost: CI runs this package
// under the race detector, whose instrumentation moves allocation counts.
func TestUnchangedWriteSetFlushEncodesASessionOnce(t *testing.T) {
	for _, withHealthCheck := range []bool{false, true} {
		s, _ := openCountedStore(t)
		sess := healthCheckedSession()
		if !withHealthCheck {
			sess.LastHealthCheck = time.Time{}
		}
		if err := s.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
		noop := func(*Session) error { return nil }
		pass := func() {
			w := s.NewWriteSet()
			if err := w.UpdateSession(sess.Key(), noop); err != nil {
				t.Fatal(err)
			}
			if err := w.Flush(); err != nil {
				t.Fatal(err)
			}
		}
		pass() // first sight of the key writes
		before := encodesOf(s)
		pass()
		if n := encodesOf(s) - before; n != 1 {
			t.Errorf("an unchanged pass (LastHealthCheck set: %v) encoded the session %d times in Flush, want 1", withHealthCheck, n)
		}
	}
}

func TestUnchangedStoreUpdateEncodesASessionOnce(t *testing.T) {
	for _, withHealthCheck := range []bool{false, true} {
		s, _ := openCountedStore(t)
		sess := healthCheckedSession()
		if !withHealthCheck {
			sess.LastHealthCheck = time.Time{}
		}
		if err := s.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
		noop := func(*Session) error { return nil }
		if err := s.UpdateSession(sess.Key(), noop); err != nil {
			t.Fatal(err)
		}
		before := encodesOf(s)
		if err := s.UpdateSession(sess.Key(), noop); err != nil {
			t.Fatal(err)
		}
		if n := encodesOf(s) - before; n != 1 {
			t.Errorf("an unchanged update (LastHealthCheck set: %v) encoded the session %d times, want 1", withHealthCheck, n)
		}
	}
}

// BenchmarkUnchangedFlush is the allocation benchmark for the per-tick compare:
// one running session carrying a LastHealthCheck, queued and flushed with
// nothing changed, as the health tick does for each seat.
func BenchmarkUnchangedFlush(b *testing.B) {
	s := NewStore()
	if err := s.OpenBolt(b.TempDir() + "/marvel.bolt"); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.CloseBolt() })
	sess := healthCheckedSession()
	if err := s.CreateSession(sess); err != nil {
		b.Fatal(err)
	}
	noop := func(*Session) error { return nil }
	w := s.NewWriteSet()
	_ = w.UpdateSession(sess.Key(), noop)
	_ = w.Flush()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := s.NewWriteSet()
		_ = w.UpdateSession(sess.Key(), noop)
		_ = w.Flush()
	}
}
