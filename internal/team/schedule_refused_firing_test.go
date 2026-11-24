package team

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// A firing whose spawn is refused leaves no session, so it used to leave no run
// row either: the schedule read current, with nothing run and nothing said but
// a rate-limited placement event the ring could roll over. The firing now
// leaves a trace at the schedule level, once, and the schedule does not stay
// silent. on_failure = wait changes nothing about it: wait holds nothing.
func TestARefusedFiringLeavesATraceAtTheScheduleLevel(t *testing.T) {
	rt := shRuntime("exit 0")
	rt.Env = map[string]string{"CLAUDE_CONFIG_DIR": api.EnvPlaceholder}
	role := clockRole("Etc/UTC", rt, nil) // on_failure = wait, retries 0
	r := newClockRig(t, "test-clock-refused", time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC), role)
	r.ctrl.ReconcileOnce()
	r.clock.Advance(17*time.Minute + 30*time.Second)
	for i := 0; i < 5; i++ {
		r.ctrl.ReconcileOnce()
	}
	if n := len(r.sessions()); n != 0 {
		t.Fatalf("%d session(s) exist, want none: the spawn is refused", n)
	}
	st := r.status()
	if st.Firing == "" {
		t.Fatalf("no firing became current: %+v", st)
	}
	skipped := r.kind(events.KindScheduleSkipped)
	if len(skipped) != 1 {
		t.Fatalf("%d schedule.skipped events after five passes, want exactly one for the firing", len(skipped))
	}
	for _, want := range []string{st.Firing, "CLAUDE_CONFIG_DIR", api.EnvPlaceholder} {
		if !strings.Contains(skipped[0].Message, want) {
			t.Errorf("event message %q lacks %q", skipped[0].Message, want)
		}
	}
	if len(st.History) != 1 || st.History[0].Outcome != api.RunFailed || st.History[0].Firing != st.Firing ||
		!strings.Contains(st.History[0].Result, "CLAUDE_CONFIG_DIR") {
		t.Errorf("history = %+v, want one failed row for the firing that names the key", st.History)
	}
	if !st.Settled {
		t.Errorf("firing not settled after a refusal with no retries: %+v", st)
	}
}

// Only the placeholder refusal is recorded as a failed run. A refusal that can
// clear by itself, a missing working directory that appears a minute later,
// still skips the way it did before: it records no run, spends no retry and
// cannot freeze a schedule under on_failure = freeze, and the seat starts on
// the pass after the directory exists.
func TestATransientRefusalAtAFiringIsNotRecordedAndCannotFreeze(t *testing.T) {
	missing := t.TempDir() + "/not-yet"
	role := clockRole("Etc/UTC", shRuntime("exit 0"), func(p *api.SchedulePolicy) {
		p.OnFailure = api.ScheduleOnFailureFreeze
	})
	role.WorkDir = missing
	r := newClockRig(t, "test-clock-transient", time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC), role)
	r.ctrl.ReconcileOnce()
	r.clock.Advance(17*time.Minute + 30*time.Second)
	for i := 0; i < 5; i++ {
		r.ctrl.ReconcileOnce()
	}
	if n := len(r.sessions()); n != 0 {
		t.Fatalf("%d session(s) exist, want none: the directory is missing", n)
	}
	st := r.status()
	if st.Firing == "" {
		t.Fatalf("no firing became current: %+v", st)
	}
	if len(st.History) != 0 {
		t.Errorf("history = %+v, want none: a transient refusal is not a run", st.History)
	}
	if st.Frozen || st.Settled {
		t.Errorf("frozen %v settled %v, want neither: the firing is still waiting to start", st.Frozen, st.Settled)
	}
	if n := len(r.kind(events.KindScheduleFrozen)); n != 0 {
		t.Errorf("%d schedule.frozen event(s), want none", n)
	}
	if n := len(r.kind(events.KindScheduleSkipped)); n != 0 {
		t.Errorf("%d schedule.skipped event(s) for a transient refusal, want none", n)
	}
	// The control: once the directory exists the same firing starts.
	if err := os.Mkdir(missing, 0o700); err != nil {
		t.Fatal(err)
	}
	r.settle(t, "the firing starts once the directory exists", func() bool { return len(r.sessions()) > 0 })
}
