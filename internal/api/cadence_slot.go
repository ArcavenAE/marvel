package api

import (
	"errors"
	"time"
)

// A declared cadence's slot records (docs/design/declared-cadence.md V2). Only
// marvel writes them, from the slot clock. T3 writes the two states below; the
// claim, the wake and the overdue state belong to later tickets and are not
// here.

// CadenceSlotState is where a slot is. It is an open enum: a record carries
// only the states the build that wrote it knew.
type CadenceSlotState string

const (
	// CadenceSlotOpen is a due slot nothing has claimed yet. V5 calls the
	// state "fresh"; T4, which owns the record list, maps it.
	CadenceSlotOpen CadenceSlotState = "open"
	// CadenceSlotSkipped is a due time marvel will not serve: it was missed
	// past starting_deadline, or the team's posture withheld it.
	CadenceSlotSkipped CadenceSlotState = "skipped"
)

// Why a slot was skipped.
const (
	CadenceSkipPastDeadline = "past-starting-deadline"
	CadenceSkipPosture      = "posture"
)

// CadenceSlot is one due time of one cadence.
type CadenceSlot struct {
	// ID names the slot by its due time (FiringID), so a claim or a wake
	// names one slot by a stable id.
	ID    string           `json:"id"`
	DueAt time.Time        `json:"due_at"`
	State CadenceSlotState `json:"state"`
	// RecordedAt is when marvel wrote the record.
	RecordedAt time.Time `json:"recorded_at,omitzero"`
	// CatchUp marks an open slot that came from a recovery: the daemon was
	// down or the tick was late when it fell due.
	CatchUp bool `json:"catch_up,omitempty"`
	// Missed is how many due times the record stands for besides the
	// newest on an open slot (N-1), and all of them on a skipped one (N).
	Missed int `json:"missed,omitempty"`
	// Reason says why a slot was skipped.
	Reason string `json:"reason,omitempty"`
}

// CadenceStatus is the slot clock's state for one declared cadence. Key is
// workspace/team/role/name.
type CadenceStatus struct {
	Key string `json:"key"`
	// SpecKey is the schedule and zone NextDueAt was computed from, so a
	// changed cadence is recomputed from now and fires nothing in the past.
	SpecKey   string    `json:"spec_key,omitempty"`
	NextDueAt time.Time `json:"next_due_at,omitzero"`
	// Slots is oldest first, bounded to the newest DefaultScheduleHistoryMax.
	Slots []CadenceSlot `json:"slots,omitempty"`
}

// errCadenceStatusNotBuilt keeps the red commit honest: the store does not
// persist a cadence status yet.
var errCadenceStatusNotBuilt = errors.New("cadence status is not built yet")

// GetCadenceStatus returns a copy of one cadence's status.
func (s *Store) GetCadenceStatus(key string) (CadenceStatus, bool) { return CadenceStatus{}, false }

// ListCadenceStatus returns a copy of every cadence status.
func (s *Store) ListCadenceStatus() []CadenceStatus { return nil }

// UpdateCadenceStatus applies fn to one cadence's status under the store lock,
// creating the record when it does not exist; only a change is written.
func (s *Store) UpdateCadenceStatus(key string, fn func(*CadenceStatus) bool) (CadenceStatus, error) {
	return CadenceStatus{}, errCadenceStatusNotBuilt
}

// DeleteCadenceStatus removes one cadence's status. Deleting a key that does
// not exist is not an error.
func (s *Store) DeleteCadenceStatus(key string) error { return nil }

// AddSlot appends a slot and drops the oldest beyond max.
func (st *CadenceStatus) AddSlot(slot CadenceSlot, max int) {}

// CadenceStep is what one step of the clock decided.
type CadenceStep struct {
	Changed bool
	// Opened and Skipped are the slot the step wrote, if any.
	Opened  *CadenceSlot
	Skipped *CadenceSlot
}

// Step advances one cadence against its stored status at now. withheld is the
// team's posture withholding. It never spawns and never touches a seat.
func (st *CadenceStatus) Step(c Cadence, now time.Time, withheld bool) (CadenceStep, error) {
	return CadenceStep{}, nil
}
