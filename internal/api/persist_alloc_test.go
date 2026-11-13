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

// unchangedFlushAllocs is the allocations of one WriteSet pass over a session
// whose callback and bytes are unchanged, for a session with or without a
// LastHealthCheck.
func unchangedFlushAllocs(t *testing.T, withHealthCheck bool) float64 {
	t.Helper()
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
	return testing.AllocsPerRun(50, pass)
}

func unchangedUpdateAllocs(t *testing.T, withHealthCheck bool) float64 {
	t.Helper()
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
	return testing.AllocsPerRun(50, func() {
		if err := s.UpdateSession(sess.Key(), noop); err != nil {
			t.Fatal(err)
		}
	})
}

// The second encode exists only for a session with a LastHealthCheck, so the
// pair of runs differs by exactly that encode. After the change the pair is
// equal: the compare image is encoded once and the full record only on a write.
func TestUnchangedWriteSetFlushEncodesASessionOnce(t *testing.T) {
	without := unchangedFlushAllocs(t, false)
	with := unchangedFlushAllocs(t, true)
	t.Logf("unchanged Flush: %.0f allocations without a LastHealthCheck, %.0f with", without, with)
	if with > without+1 {
		t.Errorf("a session with a LastHealthCheck cost %.0f allocations in Flush against %.0f without: the record is encoded twice", with, without)
	}
}

func TestUnchangedStoreUpdateEncodesASessionOnce(t *testing.T) {
	without := unchangedUpdateAllocs(t, false)
	with := unchangedUpdateAllocs(t, true)
	t.Logf("unchanged UpdateSession: %.0f allocations without a LastHealthCheck, %.0f with", without, with)
	if with > without+1 {
		t.Errorf("a session with a LastHealthCheck cost %.0f allocations in UpdateSession against %.0f without: the record is encoded twice", with, without)
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
