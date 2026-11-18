package api

import (
	"path/filepath"
	"testing"
	"time"
)

// marvel#777: UpdateSessionSpend never wrote the session, so a restart showed
// the spend as of the last unrelated persist. The ruling is a write at most
// every SpendPersistInterval per session, the first update of a session in the
// process written at once, and a flush at shutdown and re-exec. These tests
// drive a fake clock and never sleep.

// spendClock is a clock the test moves by hand.
type spendClock struct{ now time.Time }

func (c *spendClock) Now() time.Time          { return c.now }
func (c *spendClock) advance(d time.Duration) { c.now = c.now.Add(d) }

func newSpendClock() *spendClock {
	return &spendClock{now: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)}
}

func spendOf(out int) SessionSpend {
	prompt := out / 2
	return SessionSpend{Out: &out, PromptTokens: &prompt}
}

// spendStore opens a bolt-backed store with the fake clock and n running
// sessions named agent-0 to agent-(n-1).
func spendStore(t *testing.T, n int) (*Store, *spendClock, string, []string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	s := NewStore()
	if err := s.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt: %v", err)
	}
	clk := newSpendClock()
	s.SetSpendClock(clk.Now)
	var keys []string
	for i := 0; i < n; i++ {
		sess := &Session{Name: "agent-" + string(rune('0'+i)), Workspace: "ws", Team: "team", Role: "worker", State: SessionRunning, PaneID: "%1"}
		if err := s.CreateSession(sess); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		keys = append(keys, sess.Key())
	}
	return s, clk, path, keys
}

// restartedSpend closes the store as a crash or a plain close would, with no
// flush, opens the file again and returns the session's SpendOut ("nil" as -1).
func restartedSpend(t *testing.T, s *Store, path, key string) int {
	t.Helper()
	if err := s.CloseBolt(); err != nil {
		t.Fatalf("CloseBolt: %v", err)
	}
	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #2: %v", err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	got, err := s2.GetSession(key)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.SpendOut == nil {
		return -1
	}
	return *got.SpendOut
}

// The first spend update of a session in this process is written at once, so a
// crash right after the first fold still leaves a lower bound.
func TestFirstSpendUpdateIsPersistedAtOnce(t *testing.T) {
	s, _, path, keys := spendStore(t, 1)
	s.UpdateSessionSpend(keys[0], spendOf(1000))
	if got := restartedSpend(t, s, path, keys[0]); got != 1000 {
		t.Errorf("SpendOut after a restart = %d after one stream update, want 1000", got)
	}
}

// Updates inside the interval stay in memory: a restart shows the last written
// value, one tick short of the interval.
func TestSpendUpdatesInsideTheIntervalAreNotWritten(t *testing.T) {
	s, clk, path, keys := spendStore(t, 1)
	s.UpdateSessionSpend(keys[0], spendOf(1000))
	clk.advance(SpendPersistInterval - time.Nanosecond)
	s.UpdateSessionSpend(keys[0], spendOf(2000))
	if got := restartedSpend(t, s, path, keys[0]); got != 1000 {
		t.Errorf("SpendOut after a restart = %d, want 1000 (the first write; the update %v after it is inside the interval)", got, SpendPersistInterval-time.Nanosecond)
	}
}

// Exactly the interval after the last write, the next update is written.
func TestSpendUpdateAtTheIntervalIsWritten(t *testing.T) {
	s, clk, path, keys := spendStore(t, 1)
	s.UpdateSessionSpend(keys[0], spendOf(1000))
	clk.advance(SpendPersistInterval)
	s.UpdateSessionSpend(keys[0], spendOf(2000))
	if got := restartedSpend(t, s, path, keys[0]); got != 2000 {
		t.Errorf("SpendOut after a restart = %d, want 2000 (the update exactly %v after the last write)", got, SpendPersistInterval)
	}
}

// The interval counts from the last write, not from the first update: after a
// write at 15m, an update 14m59s later is held.
func TestSpendIntervalRestartsAtEachWrite(t *testing.T) {
	s, clk, path, keys := spendStore(t, 1)
	s.UpdateSessionSpend(keys[0], spendOf(1000))
	clk.advance(SpendPersistInterval)
	s.UpdateSessionSpend(keys[0], spendOf(2000))
	clk.advance(SpendPersistInterval - time.Nanosecond)
	s.UpdateSessionSpend(keys[0], spendOf(3000))
	if got := restartedSpend(t, s, path, keys[0]); got != 2000 {
		t.Errorf("SpendOut after a restart = %d, want 2000 (the write at the interval; the next update is inside a fresh interval)", got)
	}
}

// The interval is per session: one seat's recent write does not hold back a
// second seat's first update.
func TestSpendIntervalIsPerSession(t *testing.T) {
	s, clk, path, keys := spendStore(t, 2)
	s.UpdateSessionSpend(keys[0], spendOf(1000))
	clk.advance(time.Minute)
	s.UpdateSessionSpend(keys[1], spendOf(5000))
	if got := restartedSpend(t, s, path, keys[1]); got != 5000 {
		t.Errorf("second session SpendOut after a restart = %d, want 5000 (its first update, a minute after another seat's write)", got)
	}
}

// A flush at shutdown writes the held spend: Checkpoint is the call the daemon
// makes on detach, teardown and before a re-exec.
func TestCheckpointWritesHeldSpend(t *testing.T) {
	s, clk, path, keys := spendStore(t, 2)
	s.UpdateSessionSpend(keys[0], spendOf(1000))
	s.UpdateSessionSpend(keys[1], spendOf(4000))
	clk.advance(time.Minute)
	s.UpdateSessionSpend(keys[0], spendOf(2000))
	s.UpdateSessionSpend(keys[1], spendOf(6000))
	if err := s.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if got := restartedSpend(t, s, path, keys[0]); got != 2000 {
		t.Errorf("first session SpendOut after Checkpoint and a restart = %d, want 2000", got)
	}
}

// A session deleted between the update and the flush is not written back.
func TestCheckpointDoesNotResurrectADeletedSession(t *testing.T) {
	s, clk, path, keys := spendStore(t, 1)
	s.UpdateSessionSpend(keys[0], spendOf(1000))
	clk.advance(time.Minute)
	s.UpdateSessionSpend(keys[0], spendOf(2000))
	if err := s.DeleteSession(keys[0]); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if err := s.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if err := s.CloseBolt(); err != nil {
		t.Fatalf("CloseBolt: %v", err)
	}
	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #2: %v", err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	if _, err := s2.GetSession(keys[0]); err == nil {
		t.Error("the deleted session came back after Checkpoint and a restart")
	}
}
