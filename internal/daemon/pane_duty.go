package daemon

import (
	"log"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// firstTurnDutyTTL is how long a first-turn observation holds. It is an owner
// reading, named once here so a review can change it in one place.
const firstTurnDutyTTL = 24 * time.Hour

// firstTurnDuty and firstTurnSource are the cell and the closed-vocabulary
// source the pane pass writes: a turn seen from the seat's pane.
const (
	firstTurnDuty   = "first-turn"
	firstTurnSource = "pane:turn"
)

// recordFirstTurns writes the duty.first-turn cell from the pane pass (the
// seat-readiness producer, aae-orc-avwaj). When the pass that just ran read a
// seat's screen as a turn open (paneWorking) and the seat has no proven,
// unexpired first-turn record, the daemon records it proven from pane:turn for
// firstTurnDutyTTL. A current proof is not rewritten; a lapsed proof, or a fail
// a dialog check wrote, is replaced by the next observed turn, so the newest
// observation wins.
//
// Only a turn open counts. A fresh or idle seat proves no turn was taken, and
// a harness whose reader grounds no working state (codex) is never recorded.
// The pass reads only while someone queries (the pane cache's wanted window),
// so a first turn nobody was watching for is recorded at the next pass that
// sees one.
func (d *Daemon) recordFirstTurns(sessions []api.Session, now time.Time) {
	for _, s := range sessions {
		if s.PaneID == "" || s.State != api.SessionRunning {
			continue
		}
		r, ok := d.paneReads.get(s.Key(), now)
		if !ok || !r.at.Equal(now) || r.pane != paneWorking {
			continue
		}
		if cell, ok := d.store.SeatReadiness(s.Key())[api.DutyCellName(firstTurnDuty)]; ok &&
			cell.Value == string(api.DutyProven) && cell.ValidUntil.After(now) {
			continue
		}
		observed := now.UTC()
		if _, err := d.store.RecordDuty(api.DutyRecord{
			Seat: s.Key(), Duty: firstTurnDuty, State: api.DutyProven, Source: firstTurnSource,
			ObservedAt: observed, ValidUntil: observed.Add(firstTurnDutyTTL), SetBy: api.SetByDaemon,
		}); err != nil {
			// The refusal never echoes the seat; log the word, not the value.
			log.Printf("readiness: first-turn record refused: %v", err)
		}
	}
}
