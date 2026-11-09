package daemon

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// firstTurnCell returns the seat's duty.first-turn record, if one is stored.
func firstTurnCell(d *Daemon, seat string) (api.DutyRecord, bool) {
	for _, r := range d.store.ListDuties(seat) {
		if r.Duty == "first-turn" && !r.Withdrawn {
			return r, true
		}
	}
	return api.DutyRecord{}, false
}

func midTurnTick(t *testing.T) (*Daemon, *api.Session, time.Time) {
	t.Helper()
	d, sess := tickDaemon(t)
	d.paneOverride.(*fakePane).escapes = composerFixture(t, "3a-mid-turn-1s5.ansi.txt")
	now := time.Now().UTC()
	d.paneReads.want(now)
	return d, sess, now
}

// The pane pass sees a turn open on a stored seat, so the daemon records
// duty.first-turn as proven, from the pane vantage, with the named lifetime.
func TestDaemonRecordsFirstTurnFromPane(t *testing.T) {
	d, sess, now := midTurnTick(t)
	d.refreshPaneReads(now)

	rec, ok := firstTurnCell(d, sess.Key())
	if !ok {
		t.Fatal("no duty.first-turn record after the pane pass saw a turn open")
	}
	if rec.State != api.DutyProven {
		t.Errorf("state = %q, want proven", rec.State)
	}
	if rec.Source != "pane:turn" {
		t.Errorf("source = %q, want pane:turn (a host vantage routes ask-first)", rec.Source)
	}
	if rec.SetBy != api.SetByDaemon {
		t.Errorf("set_by = %q, want the daemon", rec.SetBy)
	}
	if got := rec.ValidUntil.Sub(rec.ObservedAt); got != firstTurnDutyTTL {
		t.Errorf("valid_until - observed_at = %v, want %v", got, firstTurnDutyTTL)
	}
	cell, ok := d.store.SeatReadiness(sess.Key())["duty.first-turn"]
	if !ok || cell.Value != "proven" || cell.Source != "pane:turn" {
		t.Errorf("SeatReadiness cell = %+v, want proven from pane:turn", cell)
	}
}

// The lifetime is 24 hours, as ruled, in one constant.
func TestFirstTurnLifetimeIsTwentyFourHours(t *testing.T) {
	if firstTurnDutyTTL != 24*time.Hour {
		t.Fatalf("firstTurnDutyTTL = %v, want 24h", firstTurnDutyTTL)
	}
}

// Only a turn open counts. A fresh or idle seat proves no turn was taken.
func TestFirstTurnIsNotRecordedForAnIdleOrFreshSeat(t *testing.T) {
	d, sess := tickDaemon(t)
	now := time.Now().UTC()
	d.paneReads.want(now)
	d.refreshPaneReads(now)
	if r, ok := d.paneReads.get(sess.Key(), now); !ok || r.pane == paneWorking {
		t.Fatalf("setup: the idle fixture read %q", r.pane)
	}
	if rec, ok := firstTurnCell(d, sess.Key()); ok {
		t.Fatalf("a seat with no turn open got %+v", rec)
	}
}

// A harness whose reader grounds no working state stays unrecorded.
func TestFirstTurnIsNotRecordedForACodexSeat(t *testing.T) {
	d, sess, now := midTurnTick(t)
	if err := d.store.UpdateSession(sess.Key(), func(s *api.Session) error {
		s.Runtime = api.Runtime{Name: "codex"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	d.refreshPaneReads(now)
	if rec, ok := firstTurnCell(d, sess.Key()); ok {
		t.Fatalf("a codex seat got %+v", rec)
	}
}

// A current proof is not rewritten on every pass, and a lapsed one is renewed.
func TestFirstTurnIsWrittenOnceWhileCurrent(t *testing.T) {
	d, sess, now := midTurnTick(t)
	d.refreshPaneReads(now)
	first, ok := firstTurnCell(d, sess.Key())
	if !ok {
		t.Fatal("no first record")
	}
	d.refreshPaneReads(now.Add(paneRefreshEvery + time.Second))
	again, _ := firstTurnCell(d, sess.Key())
	if again.Seq != first.Seq {
		t.Fatalf("a current proof was rewritten: seq %d then %d", first.Seq, again.Seq)
	}
}

// seedFirstTurn stores a duty.first-turn record directly, as an earlier pass
// or a dialog check would have.
func seedFirstTurn(t *testing.T, d *Daemon, seat string, state api.DutyState, source string, observed time.Time, ttl time.Duration) api.DutyRecord {
	t.Helper()
	rec, err := d.store.RecordDuty(api.DutyRecord{
		Seat: seat, Duty: "first-turn", State: state, Source: source,
		ObservedAt: observed, ValidUntil: observed.Add(ttl), SetBy: api.SetByDaemon,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return rec
}

// A proof past its lifetime is renewed by the next pass that sees a turn open.
func TestFirstTurnIsRenewedOnceItHasExpired(t *testing.T) {
	d, sess, now := midTurnTick(t)
	old := seedFirstTurn(t, d, sess.Key(), api.DutyProven, "pane:turn", now.Add(-2*firstTurnDutyTTL), firstTurnDutyTTL)
	d.refreshPaneReads(now)
	got, ok := firstTurnCell(d, sess.Key())
	if !ok {
		t.Fatal("the lapsed record vanished")
	}
	if got.Seq == old.Seq || !got.ValidUntil.After(now) {
		t.Fatalf("a lapsed proof was not renewed: seq %d then %d, valid_until %v, now %v", old.Seq, got.Seq, got.ValidUntil, now)
	}
	if got.State != api.DutyProven || got.Source != "pane:turn" {
		t.Errorf("renewed record = %+v, want proven from pane:turn", got)
	}
}

// A later observed turn restores a proof that a dialog check overwrote with a
// fail, as the readiness design says the newest observation wins.
func TestFirstTurnIsRestoredAfterAFail(t *testing.T) {
	d, sess, now := midTurnTick(t)
	seedFirstTurn(t, d, sess.Key(), api.DutyFail, "pane:dialog:trust", now.Add(-time.Minute), time.Hour)
	d.refreshPaneReads(now)
	got, _ := firstTurnCell(d, sess.Key())
	if got.State != api.DutyProven || got.Source != "pane:turn" {
		t.Fatalf("a fail was not restored by an observed turn: %+v", got)
	}
}

// An unexpired proof is not rewritten, even when it came from another vantage.
func TestFirstTurnUnexpiredProofIsNotRewritten(t *testing.T) {
	d, sess, now := midTurnTick(t)
	old := seedFirstTurn(t, d, sess.Key(), api.DutyProven, "seat:turn", now.Add(-time.Hour), firstTurnDutyTTL)
	d.refreshPaneReads(now)
	got, _ := firstTurnCell(d, sess.Key())
	if got.Seq != old.Seq {
		t.Fatalf("an unexpired proof was rewritten: seq %d then %d", old.Seq, got.Seq)
	}
}

// A pass that did not read the panes (the cache refresh is rate limited) must
// not record from a reading left by an earlier pass. Without the freshness
// guard, a stale mid_turn reading would overwrite a fail that a dialog check
// wrote after it, until the next refresh.
func TestFirstTurnIgnoresAReadingFromAnEarlierPass(t *testing.T) {
	d, sess, now := midTurnTick(t)
	d.refreshPaneReads(now)
	if _, ok := firstTurnCell(d, sess.Key()); !ok {
		t.Fatal("setup: the first pass recorded nothing")
	}
	// A dialog check later writes a fail over the proof.
	failAt := now.Add(2 * time.Second)
	fail := seedFirstTurn(t, d, sess.Key(), api.DutyFail, "pane:dialog:trust", failAt, time.Hour)

	// The next pass is inside paneRefreshEvery, so the tick reads no pane and the
	// cache still holds the first pass's mid_turn reading, which is still servable.
	next := now.Add(5 * time.Second)
	d.paneReads.want(next)
	d.refreshPaneReads(next)
	if r, ok := d.paneReads.get(sess.Key(), next); !ok || !r.at.Equal(now) {
		t.Fatalf("setup: the cache did not keep the earlier reading (ok=%v)", ok)
	}

	got, _ := firstTurnCell(d, sess.Key())
	if got.Seq != fail.Seq || got.State != api.DutyFail {
		t.Fatalf("a stale reading overwrote the dialog fail: %+v", got)
	}
}
