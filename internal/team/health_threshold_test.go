package team

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

func heartbeatRole(timeout time.Duration, threshold int) api.Role {
	r := busRole(timeout, threshold)
	r.HealthCheck.Type = api.HealthCheckHeartbeat
	return r
}

// health.failed fires on the tick a role reaches failure_threshold, which is
// the tick the restart policy applies (aae-orc-6zmvw). At the default threshold
// of 3 the event used to be consumed by the first failing tick and never
// emitted.
func TestHealthFailedFiresAtTheThreshold(t *testing.T) {
	store, ctrl, ring, key := busFixture(t, "test-health-threshold", heartbeatRole(time.Minute, 3), 10*time.Minute,
		time.Now().UTC().Add(-10*time.Minute), time.Time{})

	for tick := 1; tick <= 2; tick++ {
		ctrl.evaluateHealth()
		if n := len(healthEvents(t, ring)); n != 0 {
			t.Fatalf("health events = %d after tick %d of 3, want 0", n, tick)
		}
		got, err := store.GetSession(key)
		if err != nil {
			t.Fatal(err)
		}
		if got.HealthState != api.HealthUnhealthy || got.State != api.SessionRunning {
			t.Fatalf("after tick %d: health %q, state %q; want unhealthy and running", tick, got.HealthState, got.State)
		}
	}

	ctrl.evaluateHealth()
	evs := healthEvents(t, ring)
	if len(evs) != 1 {
		t.Fatalf("health events = %d after tick 3 of 3, want 1", len(evs))
	}
	if evs[0].Message != "heartbeat stale 3/3 failures" {
		t.Errorf("event message %q, want %q", evs[0].Message, "heartbeat stale 3/3 failures")
	}
	got, err := store.GetSession(key)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != api.SessionFailed {
		t.Errorf("state %q after tick 3, want failed (the policy applies on the same tick)", got.State)
	}
}

// A scheduled role skips the restart policy, so its seat stays evaluated past
// the threshold with the count still climbing. The event fires once on the
// crossing and not on every later tick.
func TestHealthFailedDoesNotReEmitPastTheThreshold(t *testing.T) {
	role := heartbeatRole(time.Minute, 2)
	role.Schedule = &api.SchedulePolicy{
		Cron: "17 6 * * *", Timezone: "UTC",
		Concurrency: api.ScheduleConcurrencyForbid, OnFailure: api.ScheduleOnFailureWait,
	}
	_, ctrl, ring, _ := busFixture(t, "test-health-reemit", role, 10*time.Minute,
		time.Now().UTC().Add(-10*time.Minute), time.Time{})

	for tick := 1; tick <= 4; tick++ {
		ctrl.evaluateHealth()
	}
	if n := len(healthEvents(t, ring)); n != 1 {
		t.Fatalf("health events = %d after 4 ticks at threshold 2, want exactly 1", n)
	}
}
