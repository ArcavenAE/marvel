package api

import (
	"encoding/json"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// marvel#777 round 2: a spend held by the interval must go out on a timer, not
// only on a later update or at Checkpoint, or a seat that goes quiet after a held
// update leaves the file behind after a crash. FlushDueSpend is the pass the
// daemon tick calls; these tests drive it with the fake clock from
// spend_persist_test.go and never sleep.

// fileSpend reads a session's SpendOut from the open bolt file without closing
// the store ("nil" as -1), so a test can look before and after a later pass.
func fileSpend(t *testing.T, s *Store, key string) int {
	t.Helper()
	got := -2
	err := s.bolt.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bucketSessions).Get([]byte(key))
		if v == nil {
			return nil
		}
		var sess Session
		if err := json.Unmarshal(v, &sess); err != nil {
			return err
		}
		got = -1
		if sess.SpendOut != nil {
			got = *sess.SpendOut
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read %s from the file: %v", key, err)
	}
	return got
}

// A quiet seat: one write, one held update, no further update. Once the interval
// has passed since the write, the pass writes the held value.
func TestTimerPassWritesAHeldSpendOfAQuietSeat(t *testing.T) {
	s, clk, path, keys := spendStore(t, 1)
	s.UpdateSessionSpend(keys[0], spendOf(1000))
	clk.advance(time.Minute)
	s.UpdateSessionSpend(keys[0], spendOf(2000))
	clk.advance(SpendPersistInterval)
	s.FlushDueSpend()
	if got := restartedSpend(t, s, path, keys[0]); got != 2000 {
		t.Errorf("SpendOut after a restart = %d, want 2000 (held update, no later update, interval passed, timer pass run)", got)
	}
}

// The pass leaves a spend inside its interval alone, and writes it once the
// interval has passed: one nanosecond short the file still holds the first
// write, one nanosecond later it holds the held update.
func TestTimerPassHoldsASpendInsideTheInterval(t *testing.T) {
	s, clk, _, keys := spendStore(t, 1)
	s.UpdateSessionSpend(keys[0], spendOf(1000))
	clk.advance(time.Minute)
	s.UpdateSessionSpend(keys[0], spendOf(2000))
	clk.advance(SpendPersistInterval - time.Minute - time.Nanosecond)
	s.FlushDueSpend()
	if got := fileSpend(t, s, keys[0]); got != 1000 {
		t.Errorf("SpendOut in the file = %d, want 1000 (the pass ran %v after the last write, inside the interval)", got, SpendPersistInterval-time.Nanosecond)
	}
	clk.advance(time.Nanosecond)
	s.FlushDueSpend()
	if got := fileSpend(t, s, keys[0]); got != 2000 {
		t.Errorf("SpendOut in the file = %d, want 2000 (the pass ran %v after the last write)", got, SpendPersistInterval)
	}
}

// Exactly the interval after the last write the pass writes, boundary included,
// as an update does.
func TestTimerPassWritesAtTheInterval(t *testing.T) {
	s, clk, path, keys := spendStore(t, 1)
	s.UpdateSessionSpend(keys[0], spendOf(1000))
	clk.advance(time.Minute)
	s.UpdateSessionSpend(keys[0], spendOf(2000))
	clk.advance(SpendPersistInterval - time.Minute)
	s.FlushDueSpend()
	if got := restartedSpend(t, s, path, keys[0]); got != 2000 {
		t.Errorf("SpendOut after a restart = %d, want 2000 (the pass ran exactly %v after the last write)", got, SpendPersistInterval)
	}
}

// A timer write starts a fresh interval: an update one nanosecond short of it is
// held, and a second pass inside it writes nothing.
func TestTimerWriteStartsAFreshInterval(t *testing.T) {
	s, clk, path, keys := spendStore(t, 1)
	s.UpdateSessionSpend(keys[0], spendOf(1000))
	clk.advance(time.Minute)
	s.UpdateSessionSpend(keys[0], spendOf(2000))
	clk.advance(SpendPersistInterval)
	s.FlushDueSpend()
	clk.advance(SpendPersistInterval - time.Nanosecond)
	s.UpdateSessionSpend(keys[0], spendOf(3000))
	s.FlushDueSpend()
	if got := restartedSpend(t, s, path, keys[0]); got != 2000 {
		t.Errorf("SpendOut after a restart = %d, want 2000 (the timer write; the later update is inside the interval that write started)", got)
	}
}

// The pass writes only the sessions that are due: one seat's held spend goes out
// while a seat that wrote a minute ago keeps its older value in the file.
func TestTimerPassWritesOnlyDueSessions(t *testing.T) {
	s, clk, _, keys := spendStore(t, 2)
	s.UpdateSessionSpend(keys[0], spendOf(1000))
	clk.advance(time.Minute)
	s.UpdateSessionSpend(keys[0], spendOf(2000))
	clk.advance(SpendPersistInterval - time.Minute)
	s.UpdateSessionSpend(keys[1], spendOf(4000))
	clk.advance(time.Minute)
	s.UpdateSessionSpend(keys[1], spendOf(5000))
	s.FlushDueSpend()
	if got := fileSpend(t, s, keys[0]); got != 2000 {
		t.Errorf("first session SpendOut in the file = %d, want 2000 (due)", got)
	}
	if got := fileSpend(t, s, keys[1]); got != 4000 {
		t.Errorf("second session SpendOut in the file = %d, want 4000 (written a minute ago, not due)", got)
	}
}

// A failed timer write leaves the spend held and the interval unstarted: once the
// file is writable again, the very next pass writes it, with no clock movement.
// The failure is injected by closing the bolt handle under the store.
func TestFailedTimerWriteLeavesTheSpendHeldAndTheIntervalUnstarted(t *testing.T) {
	s, clk, path, keys := spendStore(t, 1)
	s.UpdateSessionSpend(keys[0], spendOf(1000))
	clk.advance(time.Minute)
	s.UpdateSessionSpend(keys[0], spendOf(2000))
	clk.advance(SpendPersistInterval)
	if err := s.bolt.Close(); err != nil {
		t.Fatalf("close the handle under the store: %v", err)
	}
	s.FlushDueSpend()
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		t.Fatalf("reopen the file: %v", err)
	}
	s.bolt = db
	s.FlushDueSpend()
	if got := restartedSpend(t, s, path, keys[0]); got != 2000 {
		t.Errorf("SpendOut after a restart = %d, want 2000 (the failed pass must not start the interval, so the next pass at the same time writes)", got)
	}
}

// A failed write on the update path leaves the spend held and the interval
// unstarted: the next update, with the clock unmoved, is written at once. These
// two tests pin behavior that already held at 42343f3b, so they were not red;
// the mutants in the PR body show they bite.
func TestFailedUpdateWriteLeavesTheIntervalUnstarted(t *testing.T) {
	s, _, path, keys := spendStore(t, 1)
	if err := s.bolt.Close(); err != nil {
		t.Fatalf("close the handle under the store: %v", err)
	}
	s.UpdateSessionSpend(keys[0], spendOf(1000))
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		t.Fatalf("reopen the file: %v", err)
	}
	s.bolt = db
	s.UpdateSessionSpend(keys[0], spendOf(1500))
	if got := restartedSpend(t, s, path, keys[0]); got != 1500 {
		t.Errorf("SpendOut after a restart = %d, want 1500 (the first update failed to write, so the next one is still a first write)", got)
	}
}

// A key re-created inside the interval gets an immediate first write: the delete
// drops the old session's write time.
func TestRecreatedKeyInsideTheIntervalIsWrittenAtOnce(t *testing.T) {
	s, clk, path, keys := spendStore(t, 1)
	s.UpdateSessionSpend(keys[0], spendOf(1000))
	clk.advance(time.Minute)
	if err := s.DeleteSession(keys[0]); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	again := &Session{Name: "agent-0", Workspace: "ws", Team: "team", Role: "worker", State: SessionRunning, PaneID: "%2"}
	if err := s.CreateSession(again); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if again.Key() != keys[0] {
		t.Fatalf("re-created key = %q, want %q", again.Key(), keys[0])
	}
	s.UpdateSessionSpend(keys[0], spendOf(3000))
	if got := restartedSpend(t, s, path, keys[0]); got != 3000 {
		t.Errorf("SpendOut after a restart = %d, want 3000 (a key re-created a minute after the old write is a first write)", got)
	}
}

// A quiet timer pass must not move the write time. Write at 0, a held update at
// 1m, a timer pass at 15m writes it, a quiet pass at 30m, then an update at 40m
// is 25m after the last write and is written at once. If a pass kept a session's
// held mark after its write succeeded, the quiet pass at 30m would find an
// unchanged record, count it as written, and push the write time to 30m, so the
// update at 40m would be held. Passes on the real code; fails under the mutant
// that does not clear the held mark after a successful write.
func TestQuietTimerPassDoesNotMoveTheWriteTime(t *testing.T) {
	s, clk, _, keys := spendStore(t, 1)
	s.UpdateSessionSpend(keys[0], spendOf(1000))
	clk.advance(time.Minute)
	s.UpdateSessionSpend(keys[0], spendOf(2000))
	clk.advance(SpendPersistInterval - time.Minute)
	s.FlushDueSpend()
	if got := fileSpend(t, s, keys[0]); got != 2000 {
		t.Fatalf("SpendOut in the file after the timer pass at 15m = %d, want 2000", got)
	}
	clk.advance(SpendPersistInterval)
	s.FlushDueSpend()
	clk.advance(10 * time.Minute)
	s.UpdateSessionSpend(keys[0], spendOf(3000))
	if got := fileSpend(t, s, keys[0]); got != 3000 {
		t.Errorf("SpendOut in the file after an update 25m after the last write = %d, want 3000 (a quiet pass at 30m must not move the write time)", got)
	}
}
