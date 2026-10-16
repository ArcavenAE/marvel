package team

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// The evaluation tick computes one row per team, and the row carries the seat
// the tick found running.
func TestEvaluateHealthComputesTheTeamLoadRows(t *testing.T) {
	f := newListFixture(t, "test-load-rows", viewRole())
	f.seed(time.Hour, time.Minute, 0, 0)

	if rows := f.ctrl.TeamLoad(); len(rows) != 0 {
		t.Fatalf("rows before any tick = %v, want none", rows)
	}
	f.ctrl.evaluateHealth()

	rows := f.ctrl.TeamLoad()
	if len(rows) != 1 {
		t.Fatalf("rows = %v, want one for the one team", rows)
	}
	r := rows[0]
	if r.Workspace != "test-load-rows" || r.Team != "squad" || r.Running != 1 || len(r.Agents) != 1 {
		t.Errorf("row = %+v, want squad in test-load-rows with one running seat", r)
	}
	if r.ObservedAt.IsZero() || !r.ValidUntil.Equal(r.ObservedAt.Add(api.TeamLoadValidity)) {
		t.Errorf("row stamps = %v until %v", r.ObservedAt, r.ValidUntil)
	}
}

// The accessor hands out a copy: a caller that edits it does not edit the
// controller's rows.
func TestTeamLoadReturnsACopy(t *testing.T) {
	f := newListFixture(t, "test-load-copy", viewRole())
	f.seed(time.Hour, time.Minute, 0, 0)
	f.ctrl.evaluateHealth()
	rows := f.ctrl.TeamLoad()
	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	rows[0].Running = 99
	rows[0].Agents[0] = "edited"
	if again := f.ctrl.TeamLoad(); again[0].Running == 99 || again[0].Agents[0] == "edited" {
		t.Errorf("the controller's rows changed through the copy: %+v", again[0])
	}
}

// The rows carry each seat's tick-ring reading: the tick stamps it the way
// `get sessions` does, so a seat whose ring has watched the whole window reads
// as measured, not as a dash.
func TestTeamLoadRowsCarryTheTickRingReading(t *testing.T) {
	f := newListFixture(t, "test-load-ring", viewRole())
	sess := f.seed(time.Hour, time.Minute, 0, 0)
	if err := f.store.UpdateSession(sess.Key(), func(live *api.Session) error {
		live.Runtime.ContextFeed = api.ContextFeedStatusline
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var last time.Time
	for off := time.Duration(0); off <= 16*time.Minute; off += 2 * time.Second {
		last = ringEpoch.Add(off)
		f.ctrl.recordTick(sess.Key(), last, last)
	}
	f.ctrl.refreshTeamLoad(last)

	rows := f.ctrl.TeamLoad()
	if len(rows) != 1 {
		t.Fatalf("rows = %v, want one", rows)
	}
	if rows[0].ActiveMeasured != 1 || rows[0].ActiveMeanPct != 100 {
		t.Errorf("row = %+v, want the seat measured at 100%%", rows[0])
	}
}
