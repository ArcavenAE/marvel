package team

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// While the broker gate reports down, every bus-role seat stops beating at
// once. That is the broker's outage and not the role's fault, so the health
// path obeys the gate the spawn path already obeys (aae-orc-pzv0n): no failure
// is charged, no restart policy runs, and the hold is announced once.

// TestBusRoleFailuresAreNotChargedWhileTheGateIsDown: a bus role whose last bus
// beat is older than the timeout, ticked past FailureThreshold times while the
// gate reports down, keeps FailureCount at 0, stays running, and emits no
// health-check-failed event. The outage is announced once however many ticks it
// lasts, and a later outage announces again.
func TestBusRoleFailuresAreNotChargedWhileTheGateIsDown(t *testing.T) {
	store, ctrl, ring, key := busFixture(t, "test-bus-gate-down", busRole(time.Minute, 2), 10*time.Minute,
		time.Now().UTC(), time.Now().UTC().Add(-5*time.Minute))
	ready, reason := false, "bus 127.0.0.1:4222 is not ready"
	ctrl.BusGate = func() (bool, string) { return ready, reason }

	for tick := 0; tick < 4; tick++ {
		ctrl.evaluateHealth()
		got, err := store.GetSession(key)
		if err != nil {
			t.Fatal(err)
		}
		if got.FailureCount != 0 {
			t.Fatalf("tick %d: FailureCount = %d while the gate reports down, want 0", tick, got.FailureCount)
		}
		if got.State != api.SessionRunning {
			t.Fatalf("tick %d: state %q while the gate reports down, want running", tick, got.State)
		}
		if got.HealthState != api.HealthUnknown {
			t.Fatalf("tick %d: health %q while the gate reports down, want unknown", tick, got.HealthState)
		}
	}
	if n := len(healthEvents(t, ring)); n != 0 {
		t.Errorf("%d health-check-failed events while the gate reports down, want 0", n)
	}
	if n := countKind(ring, events.KindBusUnavailable); n != 1 {
		t.Errorf("bus.unavailable emitted %d times over 4 held ticks, want 1", n)
	}

	// The gate admits again: the stale beat is charged from here, as before.
	ready = true
	ctrl.evaluateHealth()
	got, _ := store.GetSession(key)
	if got.FailureCount != 1 || got.HealthState != api.HealthUnhealthy {
		t.Fatalf("after the gate admitted: failures %d, health %q; want 1 and unhealthy", got.FailureCount, got.HealthState)
	}
	if ctrl.busHolds["test-bus-gate-down/squad/worker"] {
		t.Error("latch still set after the gate admitted")
	}

	// A second outage announces again: one event per transition.
	ready = false
	ctrl.evaluateHealth()
	if n := countKind(ring, events.KindBusUnavailable); n != 2 {
		t.Errorf("second outage: bus.unavailable count %d, want 2", n)
	}
	got, _ = store.GetSession(key)
	if got.FailureCount != 1 {
		t.Errorf("second outage changed FailureCount to %d, want it left at 1", got.FailureCount)
	}
}

// TestBusRoleIsChargedWithNoGate: a cluster without a managed bus has a nil
// gate, and the health path is what it was before the gate reached it.
func TestBusRoleIsChargedWithNoGate(t *testing.T) {
	store, ctrl, ring, key := busFixture(t, "test-bus-no-gate", busRole(time.Minute, 2), 10*time.Minute,
		time.Now().UTC(), time.Now().UTC().Add(-5*time.Minute))

	ctrl.evaluateHealth()
	got, _ := store.GetSession(key)
	if got.FailureCount != 1 || got.HealthState != api.HealthUnhealthy {
		t.Fatalf("with no gate: failures %d, health %q; want 1 and unhealthy", got.FailureCount, got.HealthState)
	}
	if n := countKind(ring, events.KindBusUnavailable); n != 0 {
		t.Errorf("bus.unavailable emitted %d times with no gate, want 0", n)
	}
}

// TestHeartbeatRoleIsStillChargedWhileTheGateIsDown: the hold covers roles on
// the bus check type only. A heartbeat role does not depend on the broker, so a
// dead statusline is still its failure.
func TestHeartbeatRoleIsStillChargedWhileTheGateIsDown(t *testing.T) {
	role := busRole(time.Minute, 2)
	role.HealthCheck.Type = api.HealthCheckHeartbeat
	store, ctrl, _, key := busFixture(t, "test-hb-gate-down", role, 10*time.Minute,
		time.Now().UTC().Add(-5*time.Minute), time.Now().UTC())
	ctrl.BusGate = func() (bool, string) { return false, "bus down" }

	ctrl.evaluateHealth()
	got, _ := store.GetSession(key)
	if got.FailureCount != 1 || got.HealthState != api.HealthUnhealthy {
		t.Fatalf("heartbeat role with the gate down: failures %d, health %q; want 1 and unhealthy", got.FailureCount, got.HealthState)
	}
}

// TestShiftLaunchHoldsWhileTheBusGateIsDown: shiftLaunch creates the successors
// itself, and reconcile never plans the shifting role, so the gate in planRole
// does not cover it. With the gate down it creates no successor, leaves the
// shift in launching, and announces the hold once. When the gate admits, the
// latch clears and the launch goes ahead.
func TestShiftLaunchHoldsWhileTheBusGateIsDown(t *testing.T) {
	role := exposureWriter()
	role.Writes = nil // not the exposure gate's case
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ring := events.NewRing(0)
	ctrl.Events = ring
	createTeamFixture(t, store, exposureWS, "ops", []api.Role{role})
	seedWriterRow(t, store, "writer-0", 1, api.SessionRunning, "")
	if err := store.UpdateTeam(exposureWS+"/ops", func(live *api.Team) error {
		live.Generation = 2
		live.Shift = api.ShiftState{Phase: api.ShiftLaunching, Roles: []string{"writer"}, OldGeneration: 1}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetTeam(exposureWS + "/ops")
	team := &got
	ready, reason := false, "bus 127.0.0.1:4222 is not ready"
	ctrl.BusGate = func() (bool, string) { return ready, reason }

	ctrl.mu.Lock()
	for tick := 0; tick < 3; tick++ {
		ctrl.shiftLaunch(team, &role)
	}
	ctrl.mu.Unlock()
	if rows := store.ListSessionsByTeamRoleGeneration(exposureWS, "ops", "writer", 2); len(rows) != 0 {
		t.Errorf("shiftLaunch created %d successor rows while the gate reports down, want none", len(rows))
	}
	if n := countKind(ring, events.KindBusUnavailable); n != 1 {
		t.Errorf("bus.unavailable emitted %d times over 3 held ticks, want 1", n)
	}

	ready = true
	ctrl.mu.Lock()
	ctrl.shiftLaunch(team, &role)
	held := ctrl.busHolds[exposureWS+"/ops/writer"]
	ctrl.mu.Unlock()
	if held {
		t.Error("latch still set after the gate admitted")
	}
	if rows := store.ListSessionsByTeamRoleGeneration(exposureWS, "ops", "writer", 2); len(rows) != 1 {
		t.Errorf("after the gate admitted: %d successor rows, want 1", len(rows))
	}
}
