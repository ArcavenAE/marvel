package team

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// A ring that watched a quiet seat for 10 minutes before a pause must read the
// same 10 minutes after it, so every sample and the ring's own start move
// forward by the pause, capped at now.
func TestTickRingShiftMovesSamplesAndStart(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	var r TickRing
	base := now.Add(-3 * time.Hour)
	r.Record(base, base.Add(-time.Minute))
	r.Record(base.Add(time.Minute), time.Time{})
	r.Shift(150*time.Minute, now)

	if got, want := r.since, base.Add(150*time.Minute); !got.Equal(want) {
		t.Errorf("since = %v, want %v", got, want)
	}
	if got, want := r.samples[0].at, base.Add(150*time.Minute); !got.Equal(want) {
		t.Errorf("sample 0 at = %v, want %v", got, want)
	}
	if got, want := r.samples[0].contextAt, base.Add(150*time.Minute-time.Minute); !got.Equal(want) {
		t.Errorf("sample 0 contextAt = %v, want %v", got, want)
	}
	if !r.samples[1].contextAt.IsZero() {
		t.Errorf("a zero contextAt moved to %v", r.samples[1].contextAt)
	}

	var capped TickRing
	capped.Record(now.Add(-time.Minute), now.Add(-time.Minute))
	capped.Shift(time.Hour, now)
	if !capped.samples[0].at.Equal(now) || !capped.samples[0].contextAt.Equal(now) || !capped.since.Equal(now) {
		t.Errorf("a stamp shifted past now was not capped: %+v since %v", capped.samples[0], capped.since)
	}

	var none TickRing
	none.Record(base, base)
	none.Shift(-time.Hour, now)
	if !none.samples[0].at.Equal(base) {
		t.Errorf("a negative pause moved the ring to %v", none.samples[0].at)
	}
}

// The backoff deadline and the healthy streak move by the pause; the frozen
// saturation sentinel and zero values do not; the shifted record is persisted.
func TestShiftForPauseMovesBackoffAndHealthySince(t *testing.T) {
	_, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	restart := now.Add(-5 * time.Hour)
	ctrl.mu.Lock()
	ctrl.roleHealth["ws/t/backoff"] = &RoleHealth{
		RestartCount: 2, LastRestartAt: restart,
		BackoffUntil: now.Add(-2*time.Hour + 10*time.Minute), HealthySince: now.Add(-3 * time.Hour),
	}
	ctrl.roleHealth["ws/t/frozen"] = &RoleHealth{RestartCount: 9, BackoffUntil: saturationFreezeUntil}
	ctrl.roleHealth["ws/t/clean"] = &RoleHealth{}
	ctrl.mu.Unlock()

	ctrl.ShiftForPause(2*time.Hour, now)

	got, _ := ctrl.RoleHealthSnapshot("ws", "t", "backoff")
	if want := now.Add(10 * time.Minute); !got.BackoffUntil.Equal(want) {
		t.Errorf("BackoffUntil = %v, want %v (10 minutes left, kept)", got.BackoffUntil, want)
	}
	if want := now.Add(-time.Hour); !got.HealthySince.Equal(want) {
		t.Errorf("HealthySince = %v, want %v", got.HealthySince, want)
	}
	if !got.LastRestartAt.Equal(restart) {
		t.Errorf("LastRestartAt = %v, want it untouched at %v: nothing ages it", got.LastRestartAt, restart)
	}
	frozen, _ := ctrl.RoleHealthSnapshot("ws", "t", "frozen")
	if !frozen.BackoffUntil.Equal(saturationFreezeUntil) {
		t.Errorf("the saturation freeze moved to %v", frozen.BackoffUntil)
	}
	clean, _ := ctrl.RoleHealthSnapshot("ws", "t", "clean")
	if !clean.BackoffUntil.IsZero() || !clean.HealthySince.IsZero() {
		t.Errorf("zero values moved: %+v", clean)
	}
}

// A pause moves every ring a daemon held through the suspend.
func TestShiftForPauseMovesTheTickRings(t *testing.T) {
	_, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	ctrl.ringMu.Lock()
	r := &TickRing{}
	r.Record(now.Add(-3*time.Hour), now.Add(-3*time.Hour))
	ctrl.rings = map[string]*TickRing{"ws/a": r}
	ctrl.ringMu.Unlock()

	ctrl.ShiftForPause(2*time.Hour, now)

	ctrl.ringMu.Lock()
	defer ctrl.ringMu.Unlock()
	if got, want := ctrl.rings["ws/a"].samples[0].at, now.Add(-time.Hour); !got.Equal(want) {
		t.Errorf("ring sample at = %v, want %v", got, want)
	}
}

// The startup grace for a seat that has not emitted a first signal is measured
// on age excluding the pause: a seat created 10 minutes ago that spent 8 of
// them frozen is 2 minutes old, inside a 5-minute window.
func TestActivityStartupGraceExcludesThePause(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	role := &api.Role{ActivityTimeout: 5 * time.Minute}
	s := &api.Session{
		Runtime:   headlessRuntime(),
		CreatedAt: now.Add(-10 * time.Minute),
	}
	if got := evaluateActivity(s, role, now); got != api.ActivityStalled {
		t.Fatalf("control: unpaused 10-minute-old seat reads %v, want stalled", got)
	}
	s.PausedTotal = 8 * time.Minute
	if got := evaluateActivity(s, role, now); got != api.ActivityUnknown {
		t.Errorf("a seat paused for 8 of its 10 minutes reads %v, want unknown (inside the grace)", got)
	}
}

// The first-heartbeat grace is measured the same way.
func TestFirstHeartbeatGraceExcludesThePause(t *testing.T) {
	role := viewRole()
	role.HealthCheck = &api.HealthCheck{Type: api.HealthCheckHeartbeat, Timeout: 5 * time.Minute, FailureThreshold: 3}
	f := newListFixture(t, "test-pause-grace", role)
	sess := f.seed(10*time.Minute, time.Minute, 0, 0)
	if err := f.store.UpdateSession(sess.Key(), func(s *api.Session) error {
		// evaluateHealth reads the wall clock, not the fixture's.
		s.CreatedAt = time.Now().UTC().Add(-10 * time.Minute)
		s.LastHeartbeat = time.Time{}
		s.PausedTotal = 8 * time.Minute
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.ctrl.evaluateHealth()
	got, err := f.store.GetSession(sess.Key())
	if err != nil {
		t.Fatal(err)
	}
	if got.FailureCount != 0 || got.HealthState != api.HealthUnknown {
		t.Errorf("a seat paused for 8 of its 10 minutes: failures %d, health %q; want 0 and unknown (inside the grace)", got.FailureCount, got.HealthState)
	}
}

// Max age counts time not paused: a seat 9 hours old that spent 2 of them
// frozen is 7 hours old, under an 8-hour max age, and is not asked for a
// handoff.
func TestMaxAgeExcludesThePause(t *testing.T) {
	dir := t.TempDir()
	f := newListFixture(t, "test-pause-maxage", maxAgeRole(dir))
	sess := f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)
	if err := f.store.UpdateSession(sess.Key(), func(s *api.Session) error {
		s.PausedTotal = 2 * time.Hour
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.evaluate()
	if len(f.notices) != 0 {
		t.Errorf("a seat under max age once the pause is excluded was asked for a handoff: %q", f.notices)
	}
}

// The escalation names the adjusted age and says what was excluded, so the
// number shown is the number checked.
func TestMaxAgeEscalationNamesThePauseExcluded(t *testing.T) {
	dir := t.TempDir()
	f := newListFixture(t, "test-pause-maxage-msg", maxAgeRole(dir))
	sess := f.seed(testMaxAge+3*time.Hour, 10*time.Minute, 0, 0)
	if err := f.store.UpdateSession(sess.Key(), func(s *api.Session) error {
		s.PausedTotal = 30 * time.Minute
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.evaluate()
	f.clock.Advance(api.DefaultShiftHandoffWindow + time.Second)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Second)
	f.evaluate()
	f.ctrl.handoffProbes.wait()

	evs := f.ring.Snapshot(events.Filter{Kind: events.KindShiftHandoffMissing}, 0)
	if len(evs) != 1 {
		t.Fatalf("handoff-missing events = %d, want 1", len(evs))
	}
	if !strings.Contains(evs[0].Message, "(paused 30m0s excluded)") {
		t.Errorf("message %q does not say what was excluded", evs[0].Message)
	}
}

// With no pause the message is unchanged.
func TestMaxAgeEscalationSaysNothingOfAPauseWhenThereWasNone(t *testing.T) {
	dir := t.TempDir()
	f := newListFixture(t, "test-nopause-maxage-msg", maxAgeRole(dir))
	f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)
	f.evaluate()
	f.clock.Advance(api.DefaultShiftHandoffWindow + time.Second)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Second)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	evs := f.ring.Snapshot(events.Filter{Kind: events.KindShiftHandoffMissing}, 0)
	if len(evs) != 1 || strings.Contains(evs[0].Message, "paused") {
		t.Errorf("events %v: want one, with no mention of a pause", evs)
	}
}
