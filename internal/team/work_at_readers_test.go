package team

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// setWork stamps a seat's beat and its last reading change, offset from now on
// the fake clock the view notice reads.
func (f *listFixture) setWork(sess api.Session, beatAgo, changedAgo time.Duration) {
	f.t.Helper()
	f.setWorkAt(sess, f.clock.Now(), beatAgo, changedAgo)
}

func (f *listFixture) setWorkAt(sess api.Session, now time.Time, beatAgo, changedAgo time.Duration) {
	f.t.Helper()
	if err := f.store.UpdateSession(sess.Key(), func(live *api.Session) error {
		live.ContextAt = now.Add(-beatAgo)
		live.ReadingChangedAt = now.Add(-changedAgo)
		return nil
	}); err != nil {
		f.t.Fatal(err)
	}
}

// The activity ring counts a tick as active by the quiet predicate, so it must
// record the time of the last reading change and not the last beat: an idle seat
// that beats every 15 seconds would otherwise read 100 percent active (marvel#801).
// evaluateHealth reads the wall clock, so the stamps are set on it.
func TestTickRingRecordsTheReadingChangeNotTheBeat(t *testing.T) {
	f := newListFixture(t, "test-ring-workat", viewRole())
	sess := f.seed(time.Hour, time.Minute, 0, 0)
	f.setWorkAt(sess, time.Now().UTC(), 15*time.Second, 30*time.Minute)

	for i := 0; i < 3; i++ {
		f.ctrl.evaluateHealth()
	}
	active, total, _ := f.ctrl.ActiveTicks(sess.Key(), 10*time.Minute, time.Now().UTC())
	if total != 3 || active != 0 {
		t.Errorf("an idle beating seat: active = %d of %d ticks, want 0 of 3", active, total)
	}

	f.setWorkAt(sess, time.Now().UTC(), 15*time.Second, 5*time.Second)
	f.ctrl.evaluateHealth()
	if active, _, _ := f.ctrl.ActiveTicks(sess.Key(), 10*time.Minute, time.Now().UTC()); active != 1 {
		t.Errorf("a tick after a reading change: active = %d, want 1", active)
	}
}

// A view notice waits for a quiet seat. A seat that only beats is quiet, so the
// notice goes out and does not wait for max_defer.
func TestViewNoticeTreatsAnIdleBeatingSeatAsQuiet(t *testing.T) {
	f, sess := noticeFixture(t, "test-vn-workat", 5*time.Second)
	f.moved(sess, noticeShaOne)
	f.setWork(sess, 5*time.Second, 10*time.Minute)

	f.deliver()
	if len(f.notices) != 1 {
		t.Fatalf("notices = %d for a seat that beat 5s ago and last changed 10m ago, want 1", len(f.notices))
	}
}

// lastWorked is the last sign of work for the shift handoff's pause test. A seat
// with no totals stamp reads from the reading change, not from a beat.
func TestLastWorkedReadsTheReadingChangeNotTheBeat(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	s := api.Session{CreatedAt: now.Add(-2 * time.Hour)}
	s.ContextAt = now.Add(-15 * time.Second)
	s.ReadingChangedAt = now.Add(-30 * time.Minute)
	if got, want := lastWorked(s), now.Add(-30*time.Minute); !got.Equal(want) {
		t.Errorf("lastWorked = %v for a seat that beat 15s ago and last changed 30m ago, want %v", got, want)
	}
	// A producer that never wrote the stamp reads as before.
	s.ReadingChangedAt = time.Time{}
	if got, want := lastWorked(s), now.Add(-15*time.Second); !got.Equal(want) {
		t.Errorf("lastWorked = %v with no work stamp, want ContextAt %v", got, want)
	}
	// Where the seat forwards totals, they still win.
	s.TotalsChangedAt = now.Add(-10 * time.Minute)
	s.ReadingChangedAt = now.Add(-5 * time.Minute)
	if got, want := lastWorked(s), now.Add(-10*time.Minute); !got.Equal(want) {
		t.Errorf("lastWorked = %v with a totals stamp, want %v", got, want)
	}
}
