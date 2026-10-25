package api

import (
	"path/filepath"
	"testing"
	"time"
)

// Tests for the slot clock of a declared cadence (docs/design/declared-cadence.md
// V2, ticket T3). Each drives CadenceStatus.Step against a chosen clock, so due
// times and lateness are exact.

var slotEpoch = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

// dailyCadence is a 06:17 UTC cadence with a 10 minute starting_deadline.
func dailyCadence() Cadence {
	return Cadence{
		Name: "sweep", Schedule: "17 6 * * *", Timezone: "UTC", Prompt: "sweep the queue",
		StartingDeadline: 10 * time.Minute, Grace: 6 * time.Hour,
	}
}

func due(day int) time.Time { return time.Date(2026, 10, day, 6, 17, 0, 0, time.UTC) }

// armed is a status that has seen the cadence once at slotEpoch.
func armed(t *testing.T, c Cadence) *CadenceStatus {
	t.Helper()
	st := &CadenceStatus{Key: "ws/team/role/sweep"}
	step, err := st.Step(c, slotEpoch, false)
	if err != nil {
		t.Fatal(err)
	}
	if !step.Changed || !st.NextDueAt.Equal(due(21)) {
		t.Fatalf("first sight: changed=%v next=%v, want it armed for %v", step.Changed, st.NextDueAt, due(21))
	}
	return st
}

// A cadence seen for the first time, after an upgrade or an apply, computes
// its next due time from now and opens nothing in the past.
func TestCadenceFirstSightFiresNothingInThePast(t *testing.T) {
	t.Parallel()
	st := &CadenceStatus{Key: "ws/team/role/sweep"}
	step, err := st.Step(dailyCadence(), slotEpoch, false)
	if err != nil {
		t.Fatal(err)
	}
	if step.Opened != nil || step.Skipped != nil || len(st.Slots) != 0 {
		t.Fatalf("first sight wrote slots: opened=%v skipped=%v slots=%v", step.Opened, step.Skipped, st.Slots)
	}
	if !st.NextDueAt.Equal(due(21)) || st.SpecKey != "17 6 * * *|UTC" {
		t.Fatalf("next=%v spec=%q, want %v and the schedule key", st.NextDueAt, st.SpecKey, due(21))
	}
}

// A due slot opens exactly one record, named by its due time; the next tick
// opens none.
func TestCadenceDueSlotOpensOnce(t *testing.T) {
	t.Parallel()
	st := armed(t, dailyCadence())

	if step, _ := st.Step(dailyCadence(), due(21).Add(-time.Second), false); step.Changed {
		t.Fatalf("a tick before the due time changed the status: %+v", st)
	}
	step, err := st.Step(dailyCadence(), due(21).Add(2*time.Second), false)
	if err != nil {
		t.Fatal(err)
	}
	if step.Opened == nil || step.Skipped != nil {
		t.Fatalf("at the due time: opened=%v skipped=%v, want one open slot", step.Opened, step.Skipped)
	}
	got := step.Opened
	if got.ID != FiringID(due(21)) || !got.DueAt.Equal(due(21)) || got.State != CadenceSlotOpen || got.CatchUp || got.Missed != 0 {
		t.Fatalf("slot = %+v, want an on-time open slot %s", got, FiringID(due(21)))
	}
	if len(st.Slots) != 1 || st.Slots[0] != *got {
		t.Fatalf("Slots = %+v, want the one opened slot stored", st.Slots)
	}
	if !st.NextDueAt.Equal(due(22)) {
		t.Fatalf("next = %v, want %v", st.NextDueAt, due(22))
	}
	if step, _ = st.Step(dailyCadence(), due(21).Add(4*time.Second), false); step.Changed || len(st.Slots) != 1 {
		t.Fatalf("the next tick opened another slot: %+v", st.Slots)
	}
}

// Down across three due times and back inside starting_deadline of the newest:
// one catch-up slot for the newest, with the earlier two counted.
func TestCadenceCatchUpWithinStartingDeadline(t *testing.T) {
	t.Parallel()
	st := armed(t, dailyCadence())
	step, err := st.Step(dailyCadence(), due(23).Add(5*time.Minute), false)
	if err != nil {
		t.Fatal(err)
	}
	if step.Opened == nil || step.Skipped != nil {
		t.Fatalf("opened=%v skipped=%v, want one catch-up slot", step.Opened, step.Skipped)
	}
	if got := step.Opened; got.ID != FiringID(due(23)) || !got.CatchUp || got.Missed != 2 || got.State != CadenceSlotOpen {
		t.Fatalf("slot = %+v, want the newest due time %s as a catch-up that counts 2 earlier", got, FiringID(due(23)))
	}
	if len(st.Slots) != 1 || !st.NextDueAt.Equal(due(24)) {
		t.Fatalf("slots=%d next=%v, want 1 and %v", len(st.Slots), st.NextDueAt, due(24))
	}
}

// Past starting_deadline of even the newest missed due time: one skipped
// record for it, counting all three, and no open slot.
func TestCadenceMissedPastStartingDeadlineIsSkippedOnce(t *testing.T) {
	t.Parallel()
	st := armed(t, dailyCadence())
	step, err := st.Step(dailyCadence(), due(23).Add(30*time.Minute), false)
	if err != nil {
		t.Fatal(err)
	}
	if step.Skipped == nil || step.Opened != nil {
		t.Fatalf("opened=%v skipped=%v, want one skipped record", step.Opened, step.Skipped)
	}
	if got := step.Skipped; got.ID != FiringID(due(23)) || got.State != CadenceSlotSkipped || got.Reason != CadenceSkipPastDeadline || got.Missed != 3 {
		t.Fatalf("skipped = %+v, want %s past-starting-deadline counting 3", got, FiringID(due(23)))
	}
	for _, s := range st.Slots {
		if s.State == CadenceSlotOpen {
			t.Fatalf("an open slot exists after a skip: %+v", st.Slots)
		}
	}
	if !st.NextDueAt.Equal(due(24)) {
		t.Fatalf("next = %v, want %v", st.NextDueAt, due(24))
	}
}

// A posture-withheld team does not wait: the clock advances and records a
// skipped slot, as the schedule clock does.
func TestCadencePostureWithheldIsSkippedNotHeld(t *testing.T) {
	t.Parallel()
	st := armed(t, dailyCadence())
	step, err := st.Step(dailyCadence(), due(21).Add(2*time.Second), true)
	if err != nil {
		t.Fatal(err)
	}
	if step.Skipped == nil || step.Opened != nil || step.Skipped.Reason != CadenceSkipPosture || step.Skipped.State != CadenceSlotSkipped {
		t.Fatalf("opened=%v skipped=%+v, want one skipped (posture)", step.Opened, step.Skipped)
	}
	if !st.NextDueAt.Equal(due(22)) {
		t.Fatalf("next = %v, want the clock advanced to %v, not held", st.NextDueAt, due(22))
	}
	// Convergence later does not bring the skipped slot back.
	if step, _ = st.Step(dailyCadence(), due(21).Add(time.Minute), false); step.Changed || len(st.Slots) != 1 {
		t.Fatalf("a later tick changed the status: %+v", st.Slots)
	}
}

// A changed schedule or zone is recomputed from now and fires nothing in the
// past.
func TestCadenceChangedSpecRecomputesFromNow(t *testing.T) {
	t.Parallel()
	st := armed(t, dailyCadence())
	changed := dailyCadence()
	changed.Schedule = "45 9 * * *"
	step, err := st.Step(changed, due(25), false)
	if err != nil {
		t.Fatal(err)
	}
	if step.Opened != nil || step.Skipped != nil || len(st.Slots) != 0 {
		t.Fatalf("a changed spec wrote slots: %+v", st.Slots)
	}
	want := time.Date(2026, 10, 25, 9, 45, 0, 0, time.UTC)
	if !st.NextDueAt.Equal(want) || st.SpecKey != "45 9 * * *|UTC" {
		t.Fatalf("next=%v spec=%q, want %v recomputed from now", st.NextDueAt, st.SpecKey, want)
	}
}

// A zone with daylight saving opens one slot a day through the fall-back
// night, when 01:30 happens twice: the fixed-time rule fires the first one.
func TestCadenceFallBackDayOpensOneSlot(t *testing.T) {
	t.Parallel()
	c := Cadence{Name: "late", Schedule: "30 1 * * *", Timezone: "America/New_York", Prompt: "p", StartingDeadline: 10 * time.Minute, Grace: 6 * time.Hour}
	st := &CadenceStatus{Key: "ws/team/role/late"}
	start := time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC) // Nov 1 2026 fall-back day, 23:00 EDT the day before
	if _, err := st.Step(c, start.Add(-24*time.Hour), false); err != nil {
		t.Fatal(err)
	}
	opened := 0
	for at := start.Add(-24 * time.Hour); at.Before(start.Add(30 * time.Hour)); at = at.Add(time.Minute) {
		step, err := st.Step(c, at, false)
		if err != nil {
			t.Fatal(err)
		}
		if step.Opened != nil {
			opened++
		}
	}
	// 01:30 on Oct 31 (EDT), Nov 1 (the doubled hour, once) and Nov 2 (EST).
	if opened != 3 || len(st.Slots) != 3 {
		t.Fatalf("opened %d slots, stored %d across 54 hours, want 3 (one per local day, the doubled 01:30 once)", opened, len(st.Slots))
	}
}

// The history keeps the newest slots and drops the oldest.
func TestCadenceHistoryKeepsTheNewest(t *testing.T) {
	t.Parallel()
	st := &CadenceStatus{}
	for i := 0; i < 7; i++ {
		d := slotEpoch.Add(time.Duration(i) * time.Hour)
		st.AddSlot(CadenceSlot{ID: FiringID(d), DueAt: d, State: CadenceSlotOpen}, 5)
	}
	if len(st.Slots) != 5 || st.Slots[0].ID != FiringID(slotEpoch.Add(2*time.Hour)) || st.Slots[4].ID != FiringID(slotEpoch.Add(6*time.Hour)) {
		t.Fatalf("Slots = %+v, want the newest five, oldest first", st.Slots)
	}
}

// Slots survive a daemon restart, and the store hands out copies.
func TestCadenceStatusSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	s1 := NewStore()
	if err := s1.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	key := "ws/team/role/sweep"
	slot := CadenceSlot{ID: FiringID(due(21)), DueAt: due(21), State: CadenceSlotOpen, RecordedAt: due(21).Add(time.Second), CatchUp: true, Missed: 2}
	if _, err := s1.UpdateCadenceStatus(key, func(st *CadenceStatus) bool {
		st.SpecKey, st.NextDueAt = "17 6 * * *|UTC", due(22)
		st.AddSlot(slot, DefaultScheduleHistoryMax)
		return true
	}); err != nil {
		t.Fatalf("UpdateCadenceStatus: %v", err)
	}
	got, ok := s1.GetCadenceStatus(key)
	if !ok || len(got.Slots) != 1 {
		t.Fatalf("Get after Update = %+v, %v", got, ok)
	}
	got.Slots[0].ID = "mutated"
	if again, _ := s1.GetCadenceStatus(key); again.Slots[0].ID != slot.ID {
		t.Fatal("the store handed out its own slice")
	}
	if err := s1.CloseBolt(); err != nil {
		t.Fatal(err)
	}

	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	st, ok := s2.GetCadenceStatus(key)
	if !ok || st.SpecKey != "17 6 * * *|UTC" || !st.NextDueAt.Equal(due(22)) || len(st.Slots) != 1 || st.Slots[0] != slot {
		t.Fatalf("after reopen = %+v, %v; want the status and its slot back", st, ok)
	}
	if n := len(s2.ListCadenceStatus()); n != 1 {
		t.Fatalf("ListCadenceStatus = %d, want 1", n)
	}
	if err := s2.DeleteCadenceStatus(key); err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.GetCadenceStatus(key); ok {
		t.Fatal("status still present after Delete")
	}
	if err := s2.DeleteCadenceStatus(key); err != nil {
		t.Fatalf("deleting a missing status: %v", err)
	}
}
