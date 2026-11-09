package daemon

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// An idle statusline seat stamps ContextAt on every beat and ReadingChangedAt
// only when its reading moves. The watchdog's quiet gate and its did-work clear
// read the second (marvel#801).

func (r *wdRig) setWork(ctxAt, changedAt time.Time) {
	r.t.Helper()
	err := r.store.UpdateSession("ws/a", func(s *api.Session) error {
		s.ContextPercent, s.ContextAt, s.ReadingChangedAt = 1, ctxAt, changedAt
		return nil
	})
	if err != nil {
		r.t.Fatal(err)
	}
}

// A seat that beats every 15 seconds with an unchanged reading is quiet, so the
// watchdog reads its pane.
func TestWatchdogReadsAnIdleBeatingSeatAsQuiet(t *testing.T) {
	r := newWDRig(t)
	p := r.seat("a", "claude", "2.1.283", 60*time.Minute, 0)
	r.setWork(r.now.Add(-15*time.Second), r.now.Add(-30*time.Minute))
	r.w.Once()
	if p.caps != 1 {
		t.Fatalf("captures = %d for a seat whose reading last changed 30 minutes ago, want 1", p.caps)
	}
}

// A real change leaves the seat unread, as before.
func TestWatchdogLeavesASeatWithAFreshReadingChange(t *testing.T) {
	r := newWDRig(t)
	p := r.seat("a", "claude", "2.1.283", 60*time.Minute, 0)
	r.setWork(r.now.Add(-15*time.Second), r.now.Add(-2*time.Minute))
	r.w.Once()
	if p.caps != 0 {
		t.Fatalf("captures = %d for a seat that changed 2 minutes ago, want 0", p.caps)
	}
}

// Beats alone do not clear a recorded state; a reading change does.
func TestWatchdogClearsOnAReadingChangeNotOnABeat(t *testing.T) {
	r := newWDRig(t)
	r.seat("a", "claude", "2.1.283", 60*time.Minute, 0)
	changed := r.now.Add(-30 * time.Minute)
	r.setWork(changed, changed)
	r.w.Once()
	if r.get("a").HarnessState == nil {
		t.Fatal("state not set")
	}
	r.now = r.now.Add(time.Minute)
	r.setWork(r.now, changed)
	r.w.Once()
	if r.get("a").HarnessState == nil {
		t.Fatal("a repeated reading cleared the state")
	}
	r.now = r.now.Add(time.Minute)
	r.setWork(r.now, r.now)
	r.w.Once()
	if r.get("a").HarnessState != nil {
		t.Fatal("a changed reading did not clear the state")
	}
}
