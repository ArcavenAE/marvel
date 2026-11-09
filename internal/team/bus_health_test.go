package team

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// busRole is a one-replica role on the bus healthcheck. RestartNever makes the
// restart policy observable without tmux: at the failure threshold the seat
// reads failed and the role freezes.
func busRole(timeout time.Duration, threshold int) api.Role {
	return api.Role{
		Name: "worker", Replicas: 1, RestartPolicy: api.RestartNever,
		Runtime:     api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
		HealthCheck: &api.HealthCheck{Type: api.HealthCheckBus, Timeout: timeout, FailureThreshold: threshold},
	}
}

// busFixture seeds one running seat, created `age` ago, with the given stamps.
func busFixture(t *testing.T, ws string, role api.Role, age time.Duration, lastHeartbeat, lastBusBeat time.Time) (*api.Store, *Controller, *events.Ring, string) {
	t.Helper()
	store := api.NewStore()
	ctrl := NewController(store, nil)
	ring := events.NewRing(0)
	ctrl.Events = ring
	createTeamFixture(t, store, ws, "squad", []api.Role{role})
	sess := api.Session{
		Name: "squad-worker-g1-0", Workspace: ws, Team: "squad", Role: "worker",
		Generation: 1, State: api.SessionRunning,
		CreatedAt:     time.Now().UTC().Add(-age),
		LastHeartbeat: lastHeartbeat, LastBusBeat: lastBusBeat,
	}
	seedSession(t, store, sess)
	return store, ctrl, ring, sess.Key()
}

func healthEvents(t *testing.T, ring *events.Ring) []events.Event {
	t.Helper()
	return ring.Snapshot(events.Filter{Kind: events.KindHealthCheckFailed}, 0)
}

// The key red test (aae-orc-z63a4, finding-166): statusline heartbeats keep
// arriving and no bus beat does. A role on the bus type goes unhealthy after
// the timeout, and the restart policy applies at the threshold. Under the
// heartbeat type this seat would read healthy for as long as the statusline
// beats, which is the finding.
func TestBusRoleGoesUnhealthyWhileStatuslineBeatsArrive(t *testing.T) {
	store, ctrl, _, key := busFixture(t, "test-bus-red", busRole(time.Minute, 2), 10*time.Minute,
		time.Now().UTC(), time.Time{})

	ctrl.evaluateHealth()
	got, err := store.GetSession(key)
	if err != nil {
		t.Fatal(err)
	}
	if got.HealthState != api.HealthUnhealthy || got.FailureCount != 1 {
		t.Fatalf("after one tick: health %q, failures %d; want unhealthy and 1 (a live statusline must not vouch for the bus)",
			got.HealthState, got.FailureCount)
	}
	if got.State != api.SessionRunning {
		t.Fatalf("state %q after one failure of two, want running", got.State)
	}

	// The statusline beats again between ticks. It still must not matter.
	if err := store.UpdateSession(key, func(s *api.Session) error {
		s.LastHeartbeat = time.Now().UTC()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctrl.evaluateHealth()
	got, err = store.GetSession(key)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != api.SessionFailed {
		t.Fatalf("state %q at the failure threshold, want failed (restart policy applied)", got.State)
	}
}

// The failure event names its cause, so an operator reading the ring sees that
// the director shim lost the bus. Threshold 2: the first tick marks the seat
// unhealthy and emits nothing, and the tick that reaches the threshold emits
// once (aae-orc-6zmvw).
func TestBusRoleEventNamesTheMissingBusBeat(t *testing.T) {
	_, ctrl, ring, _ := busFixture(t, "test-bus-event", busRole(time.Minute, 2), 10*time.Minute,
		time.Now().UTC(), time.Time{})
	ctrl.evaluateHealth()
	if n := len(healthEvents(t, ring)); n != 0 {
		t.Fatalf("health events = %d after tick 1 of 2, want 0", n)
	}
	ctrl.evaluateHealth()
	evs := healthEvents(t, ring)
	if len(evs) != 1 {
		t.Fatalf("health events = %d after tick 2 of 2, want 1", len(evs))
	}
	if !strings.Contains(evs[0].Message, "no bus beat from the director shim for") {
		t.Errorf("event message %q does not name the missing bus beat", evs[0].Message)
	}
}

// The control for the red test: the same seat with a fresh bus beat and a dead
// statusline reads healthy, so the type reads LastBusBeat and only that.
func TestBusRoleReadsHealthyOnABusBeatAlone(t *testing.T) {
	store, ctrl, _, key := busFixture(t, "test-bus-green", busRole(time.Minute, 2), 10*time.Minute,
		time.Time{}, time.Now().UTC())
	ctrl.evaluateHealth()
	got, err := store.GetSession(key)
	if err != nil {
		t.Fatal(err)
	}
	if got.HealthState != api.HealthHealthy || got.FailureCount != 0 {
		t.Errorf("health %q, failures %d; want healthy and 0 on a fresh bus beat with no statusline beat",
			got.HealthState, got.FailureCount)
	}
}

// A beat older than the timeout fails, and one bus beat later clears the count.
func TestBusRoleRecoversOnTheNextBusBeat(t *testing.T) {
	store, ctrl, _, key := busFixture(t, "test-bus-recover", busRole(time.Minute, 3), 10*time.Minute,
		time.Time{}, time.Now().UTC().Add(-5*time.Minute))
	ctrl.evaluateHealth()
	got, _ := store.GetSession(key)
	if got.HealthState != api.HealthUnhealthy || got.FailureCount != 1 {
		t.Fatalf("stale bus beat: health %q, failures %d; want unhealthy and 1", got.HealthState, got.FailureCount)
	}
	if err := store.UpdateSession(key, func(s *api.Session) error {
		s.LastBusBeat = time.Now().UTC()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctrl.evaluateHealth()
	got, _ = store.GetSession(key)
	if got.HealthState != api.HealthHealthy || got.FailureCount != 0 {
		t.Errorf("after a fresh bus beat: health %q, failures %d; want healthy and 0", got.HealthState, got.FailureCount)
	}
}

// Inside the grace with no beat the seat is unknown and counts no failure;
// past it, with still no beat, it is unhealthy.
func TestBusRoleGraceThenUnhealthy(t *testing.T) {
	store, ctrl, _, key := busFixture(t, "test-bus-grace", busRole(time.Minute, 3), 10*time.Second,
		time.Time{}, time.Time{})
	ctrl.evaluateHealth()
	got, _ := store.GetSession(key)
	if got.HealthState != api.HealthUnknown || got.FailureCount != 0 {
		t.Fatalf("inside the grace: health %q, failures %d; want unknown and 0", got.HealthState, got.FailureCount)
	}

	if err := store.UpdateSession(key, func(s *api.Session) error {
		s.CreatedAt = time.Now().UTC().Add(-5 * time.Minute)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctrl.evaluateHealth()
	got, _ = store.GetSession(key)
	if got.HealthState != api.HealthUnhealthy || got.FailureCount != 1 {
		t.Errorf("past the grace with no beat: health %q, failures %d; want unhealthy and 1", got.HealthState, got.FailureCount)
	}
}

// The grace excludes time the cluster was suspended, as it does for heartbeat.
func TestBusRoleGraceExcludesThePause(t *testing.T) {
	store, ctrl, _, key := busFixture(t, "test-bus-pause", busRole(5*time.Minute, 3), 10*time.Minute,
		time.Time{}, time.Time{})
	if err := store.UpdateSession(key, func(s *api.Session) error {
		s.PausedTotal = 8 * time.Minute
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctrl.evaluateHealth()
	got, _ := store.GetSession(key)
	if got.FailureCount != 0 || got.HealthState != api.HealthUnknown {
		t.Errorf("a bus seat paused for 8 of its 10 minutes: failures %d, health %q; want 0 and unknown", got.FailureCount, got.HealthState)
	}
}

// Shift readiness: a bus seat is ready only once its first bus beat has
// arrived, whatever the statusline has sent. A heartbeat role is unchanged.
func TestAllReadyWaitsForTheFirstBusBeat(t *testing.T) {
	ctrl := NewController(api.NewStore(), nil)
	role := busRole(time.Minute, 3)
	now := time.Now().UTC()
	running := func(hb, bus time.Time) []api.Session {
		return []api.Session{{State: api.SessionRunning, LastHeartbeat: hb, LastBusBeat: bus}}
	}
	if ctrl.allReady(running(now, time.Time{}), &role) {
		t.Error("a bus seat with a statusline beat and no bus beat reads ready")
	}
	if !ctrl.allReady(running(time.Time{}, now), &role) {
		t.Error("a bus seat with a bus beat reads not ready")
	}
	hbRole := api.Role{Name: "worker", HealthCheck: &api.HealthCheck{Type: api.HealthCheckHeartbeat, Timeout: time.Minute, FailureThreshold: 3}}
	if !ctrl.allReady(running(now, time.Time{}), &hbRole) {
		t.Error("a heartbeat seat with a heartbeat reads not ready; the bus change leaked into it")
	}
	if ctrl.allReady(running(time.Time{}, now), &hbRole) {
		t.Error("a heartbeat seat with only a bus beat reads ready; a bus beat must not stand in for a heartbeat")
	}
}

// A heartbeat role never reads LastBusBeat: a live bus beat does not rescue a
// silent statusline, and its event text is the one it always had.
func TestHeartbeatRoleIgnoresLastBusBeat(t *testing.T) {
	role := busRole(time.Minute, 1)
	role.HealthCheck.Type = api.HealthCheckHeartbeat
	store, ctrl, ring, key := busFixture(t, "test-hb-ignores-bus", role, 10*time.Minute,
		time.Now().UTC().Add(-time.Hour), time.Now().UTC())
	ctrl.evaluateHealth()
	got, _ := store.GetSession(key)
	if got.HealthState != api.HealthUnhealthy {
		t.Fatalf("health %q, want unhealthy: a bus beat must not vouch for a heartbeat role", got.HealthState)
	}
	evs := healthEvents(t, ring)
	if len(evs) != 1 || evs[0].Message != "heartbeat stale 1/1 failures" {
		t.Errorf("events %+v, want one with the unchanged heartbeat text", evs)
	}
}

// The seat the ticket names (aae-orc-z63a4, finding-166): the harness runs and
// its statusline beats, but the director shim never reached the bus, so no bus
// beat ever arrives. Across the timeout the seat must never read healthy, and
// its State stays running, because running only means the pane exists. This
// is the case the "health set from running" shortcut would get wrong.
func TestBusRoleThatNeverConnectedNeverReadsHealthy(t *testing.T) {
	store, ctrl, _, key := busFixture(t, "test-bus-never", busRole(api.BusHealthTimeout, 3), time.Second,
		time.Now().UTC(), time.Time{})

	steps := []struct {
		name string
		age  time.Duration
		want api.HealthState
	}{
		{"just launched", time.Second, api.HealthUnknown},
		{"inside the timeout", api.BusHealthTimeout - time.Second, api.HealthUnknown},
		{"one tick past the timeout", api.BusHealthTimeout + time.Second, api.HealthUnhealthy},
		{"two ticks past the timeout", api.BusHealthTimeout + 2*time.Second, api.HealthUnhealthy},
	}
	for _, st := range steps {
		if err := store.UpdateSession(key, func(s *api.Session) error {
			s.CreatedAt = time.Now().UTC().Add(-st.age)
			s.LastHeartbeat = time.Now().UTC() // the statusline is alive throughout
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		ctrl.evaluateHealth()
		got, err := store.GetSession(key)
		if err != nil {
			t.Fatal(err)
		}
		if got.HealthState == api.HealthHealthy {
			t.Fatalf("%s: health healthy with no bus beat ever", st.name)
		}
		if got.HealthState != st.want {
			t.Errorf("%s: health %q, want %q", st.name, got.HealthState, st.want)
		}
		if got.State != api.SessionRunning {
			t.Errorf("%s: state %q, want running (the pane exists)", st.name, got.State)
		}
	}
}
