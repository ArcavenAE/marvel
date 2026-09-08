package api

import "testing"

var allStates = []SessionState{
	SessionPending, SessionRunning, SessionCrashLoopBackOff,
	SessionSucceeded, SessionFailed, SessionCrashed,
}

// TestOccupiesReplicaSlotMatchesLivenessForInteractive pins that the split in
// ADR-010 changes nothing for service-shaped roles: for any non-headless
// session, occupying a replica slot is exactly having a live process.
func TestOccupiesReplicaSlotMatchesLivenessForInteractive(t *testing.T) {
	t.Parallel()
	for _, mode := range []RuntimeMode{RuntimeModeInteractive, ""} {
		for _, st := range allStates {
			s := Session{State: st, Runtime: Runtime{Mode: mode}}
			if got, want := OccupiesReplicaSlot(s), st.CountsAsAlive(); got != want {
				t.Errorf("mode=%q state=%q: OccupiesReplicaSlot = %v, want %v (liveness)",
					mode, st, got, want)
			}
		}
	}
}

// TestHeadlessSucceededHoldsItsSlot is the behaviour ADR-010 ratifies: a
// finished job satisfies its replica, so the reconciler must not refill it.
func TestHeadlessSucceededHoldsItsSlot(t *testing.T) {
	t.Parallel()
	s := Session{State: SessionSucceeded, Runtime: Runtime{Mode: RuntimeModeHeadless}}
	if !OccupiesReplicaSlot(s) {
		t.Fatal("a succeeded headless session must hold its replica slot")
	}
	if s.State.CountsAsAlive() {
		t.Fatal("...while still reading as NO live process: budget admission, " +
			"posture, shift readiness and procstat must not see a finished job as alive")
	}
}

// TestHeadlessFailureModesDoNotHoldTheSlot guards the opposite error. A
// headless role that died at startup must still be replaced; only completion
// holds the slot. Crashed is the state ReapDead assigns today, so this also
// pins that the seam does not silently strand a broken role.
func TestHeadlessFailureModesDoNotHoldTheSlot(t *testing.T) {
	t.Parallel()
	for _, st := range []SessionState{SessionCrashed, SessionFailed} {
		s := Session{State: st, Runtime: Runtime{Mode: RuntimeModeHeadless}}
		if OccupiesReplicaSlot(s) {
			t.Errorf("headless state=%q must NOT hold a replica slot", st)
		}
	}
}

// TestCountReplicaSlotsIsBehaviourIdenticalToday is the load-bearing
// regression guard for landing the seam ahead of the fix. Nothing writes
// SessionSucceeded yet (aae-orc-bxeh owns the exit-status work), so for every
// session set reachable today CountReplicaSlots must equal CountAlive. If this
// starts failing, the reconciler's arithmetic has changed and that change was
// not supposed to arrive with this commit.
func TestCountReplicaSlotsIsBehaviourIdenticalToday(t *testing.T) {
	t.Parallel()
	var reachable []Session
	for _, mode := range []RuntimeMode{RuntimeModeInteractive, RuntimeModeHeadless, ""} {
		for _, st := range allStates {
			if st == SessionSucceeded {
				continue // unreachable in production until bxeh lands
			}
			reachable = append(reachable, Session{State: st, Runtime: Runtime{Mode: mode}})
		}
	}
	if got, want := CountReplicaSlots(reachable), CountAlive(reachable); got != want {
		t.Errorf("CountReplicaSlots = %d, CountAlive = %d; the seam must be a no-op "+
			"until SessionSucceeded is written", got, want)
	}
	if got := CountReplicaSlots(nil); got != 0 {
		t.Errorf("CountReplicaSlots(nil) = %d, want 0", got)
	}
}

// TestCountReplicaSlotsDivergesOnlyOnHeadlessCompletion states the one case
// where the two counts are allowed to disagree, so the divergence is
// documented rather than discovered.
func TestCountReplicaSlotsDivergesOnlyOnHeadlessCompletion(t *testing.T) {
	t.Parallel()
	sessions := []Session{
		{State: SessionRunning, Runtime: Runtime{Mode: RuntimeModeInteractive}},
		{State: SessionSucceeded, Runtime: Runtime{Mode: RuntimeModeHeadless}},
		{State: SessionSucceeded, Runtime: Runtime{Mode: RuntimeModeInteractive}},
	}
	if got := CountAlive(sessions); got != 1 {
		t.Errorf("CountAlive = %d, want 1 (only the running one is a live process)", got)
	}
	if got := CountReplicaSlots(sessions); got != 2 {
		t.Errorf("CountReplicaSlots = %d, want 2 (running + the completed headless job)", got)
	}
}
