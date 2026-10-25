package api

import (
	"fmt"
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

// cadenceOnTime is how late a slot may be opened and still count as on time;
// the reconciler ticks every two seconds, so a minute absorbs a slow tick.
const cadenceOnTime = time.Minute

func cloneCadenceStatus(st *CadenceStatus) CadenceStatus {
	out := *st
	out.Slots = append([]CadenceSlot(nil), st.Slots...)
	return out
}

// GetCadenceStatus returns a copy of one cadence's status.
func (s *Store) GetCadenceStatus(key string) (CadenceStatus, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.cadenceStatus[key]
	if !ok {
		return CadenceStatus{}, false
	}
	return cloneCadenceStatus(st), true
}

// ListCadenceStatus returns a copy of every cadence status.
func (s *Store) ListCadenceStatus() []CadenceStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]CadenceStatus, 0, len(s.cadenceStatus))
	for _, st := range s.cadenceStatus {
		out = append(out, cloneCadenceStatus(st))
	}
	return out
}

// UpdateCadenceStatus applies fn to one cadence's status under the store lock,
// creating the record when it does not exist, and returns a copy of the result.
// fn reports whether it changed anything; only a change is written to disk, so
// the reconciler can evaluate every tick without a write per tick.
func (s *Store) UpdateCadenceStatus(key string, fn func(*CadenceStatus) bool) (CadenceStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.cadenceStatus[key]
	if !ok {
		st = &CadenceStatus{Key: key}
	}
	work := cloneCadenceStatus(st)
	if !fn(&work) && ok {
		return cloneCadenceStatus(st), nil
	}
	work.Key = key
	if err := s.persistPut(bucketCadenceStatus, key, &work); err != nil {
		return cloneCadenceStatus(st), fmt.Errorf("persist cadence status %s: %w", key, err)
	}
	s.cadenceStatus[key] = &work
	return cloneCadenceStatus(&work), nil
}

// DeleteCadenceStatus removes one cadence's status. Deleting a key that does
// not exist is not an error.
func (s *Store) DeleteCadenceStatus(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.cadenceStatus[key]; !ok {
		return nil
	}
	if err := s.persistDelete(bucketCadenceStatus, key); err != nil {
		return fmt.Errorf("delete cadence status %s: %w", key, err)
	}
	delete(s.cadenceStatus, key)
	return nil
}

// AddSlot appends a slot and drops the oldest beyond max. A slot whose id is
// already the newest is not added twice.
func (st *CadenceStatus) AddSlot(slot CadenceSlot, max int) {
	if n := len(st.Slots); n > 0 && st.Slots[n-1].ID == slot.ID {
		return
	}
	st.Slots = append(st.Slots, slot)
	if max > 0 && len(st.Slots) > max {
		st.Slots = append([]CadenceSlot(nil), st.Slots[len(st.Slots)-max:]...)
	}
}

// CadenceStep is what one step of the clock decided.
type CadenceStep struct {
	Changed bool
	// Opened and Skipped are the slot the step wrote, if any.
	Opened  *CadenceSlot
	Skipped *CadenceSlot
}

// Step advances one cadence against its stored status at now. withheld is the
// team's posture withholding. It never spawns and never touches a seat.
//
// A cadence seen for the first time, or with a changed schedule or zone, takes
// its next due time from now and writes nothing in the past. When the due time
// has passed, the newest due time in (NextDueAt, now] is the one that may open
// (CatchUp counts the earlier ones): within starting_deadline of its due time
// it opens a slot, past it, or on a withheld team, it is recorded skipped.
// There is one record per outage, never one per missed due time. It builds the
// policy from the schedule and zone alone and does not report daylight-saving
// facts: a cadence has no dst_ack (V1).
func (st *CadenceStatus) Step(c Cadence, now time.Time, withheld bool) (CadenceStep, error) {
	p := SchedulePolicy{Cron: c.Schedule, Timezone: c.Timezone}
	spec := c.Schedule + "|" + c.Timezone
	if st.SpecKey != spec || st.NextDueAt.IsZero() {
		next, err := p.NextFiring(now)
		if err != nil {
			return CadenceStep{}, err
		}
		st.SpecKey, st.NextDueAt = spec, next
		return CadenceStep{Changed: true}, nil
	}
	if now.Before(st.NextDueAt) {
		return CadenceStep{}, nil
	}
	newest, missed, err := p.CatchUp(st.NextDueAt, now)
	if err != nil {
		return CadenceStep{}, err
	}
	next, err := p.NextFiring(newest)
	if err != nil {
		return CadenceStep{}, err
	}
	st.NextDueAt = next

	late := now.Sub(newest)
	slot := CadenceSlot{ID: FiringID(newest), DueAt: newest, RecordedAt: now}
	step := CadenceStep{Changed: true}
	switch {
	case withheld:
		slot.State, slot.Reason, slot.Missed = CadenceSlotSkipped, CadenceSkipPosture, missed+1
		step.Skipped = &slot
	case late > c.StartingDeadline:
		slot.State, slot.Reason, slot.Missed = CadenceSlotSkipped, CadenceSkipPastDeadline, missed+1
		step.Skipped = &slot
	default:
		slot.State, slot.Missed = CadenceSlotOpen, missed
		slot.CatchUp = missed > 0 || late > cadenceOnTime
		step.Opened = &slot
	}
	st.AddSlot(slot, DefaultScheduleHistoryMax)
	return step, nil
}
