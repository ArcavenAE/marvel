package daemon

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/suspend"
	"github.com/arcavenae/marvel/internal/team"
)

// timersRig is a held daemon with one running session that was active an hour
// before a 90-minute pause, one role in a crash-loop backoff, and a controller
// that rehydrated that role health, so a resume has every timer to move.
// pauseOfRig is the length of the pause the rig's record describes.
const pauseOfRig = 90 * time.Minute

type timersRig struct {
	*verbRig
	now   time.Time
	since time.Time
	ctrl  *team.Controller
}

func newTimersRig(t *testing.T) *timersRig {
	t.Helper()
	r := &timersRig{verbRig: newVerbRig(t)}
	r.d.loopsStarted.Store(true)
	r.now = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	r.since = r.now.Add(-pauseOfRig)
	r.d.suspendNow = func() time.Time { return r.now }

	s := &api.Session{
		Name: "a", Workspace: "ws", Team: "t", Role: "r", State: api.SessionRunning, PID: 200,
		CreatedAt: r.now.Add(-5 * time.Hour),
	}
	if err := r.d.store.CreateSession(s); err != nil {
		t.Fatal(err)
	}
	if err := r.d.store.UpdateSession(s.Key(), func(s *api.Session) error {
		s.LastHeartbeat = r.now.Add(-100 * time.Minute)
		s.ContextAt = r.now.Add(-100 * time.Minute)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := r.d.store.PersistRoleHealth(api.RoleHealthRecord{
		Key: "ws/t/r", RestartCount: 3,
		LastRestartAt: r.now.Add(-4 * time.Hour),
		BackoffUntil:  r.since.Add(4 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	r.ctrl = team.NewController(r.d.store, nil)
	if err := r.ctrl.RehydrateRoleHealth(); err != nil {
		t.Fatal(err)
	}
	r.d.teamCtrl = r.ctrl

	r.holdAsStart(t)
	rec := *r.d.suspendRec.Load()
	rec.Since = r.since
	if err := (suspendStore{r.d}).Save(rec); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *timersRig) check(t *testing.T) {
	t.Helper()
	r.checkPause(t)
	got := hb(t, r.d, "a")
	if want := r.now.Add(-10 * time.Minute); !got.LastHeartbeat.Equal(want) {
		t.Errorf("LastHeartbeat = %v, want %v", got.LastHeartbeat, want)
	}
	if want := r.now.Add(-10 * time.Minute); !got.ContextAt.Equal(want) {
		t.Errorf("ContextAt = %v, want %v", got.ContextAt, want)
	}
}

// checkPause reads only the fields a repeat must leave alone.
func (r *timersRig) checkPause(t *testing.T) {
	t.Helper()
	got := hb(t, r.d, "a")
	if got.PausedTotal != pauseOfRig {
		t.Errorf("PausedTotal = %v, want %v", got.PausedTotal, pauseOfRig)
	}
	if !got.PauseAppliedSince.Equal(r.since) {
		t.Errorf("PauseAppliedSince = %v, want %v", got.PauseAppliedSince, r.since)
	}
	if !got.CreatedAt.Equal(r.now.Add(-5 * time.Hour)) {
		t.Errorf("CreatedAt moved to %v", got.CreatedAt)
	}
}

// Resume moves every timer in one pass: the heartbeat and context stamps, the
// per-session paused total, and the role's backoff, which is persisted.
func TestResumeAppliesThePauseToEveryTimer(t *testing.T) {
	r := newTimersRig(t)
	if resp := r.call("resume", map[string]any{}); resp.Error != "" {
		t.Fatalf("resume: %s", resp.Error)
	}
	r.check(t)

	rh, ok := r.ctrl.RoleHealthSnapshot("ws", "t", "r")
	if !ok {
		t.Fatal("no role health")
	}
	if want := r.since.Add(4*time.Minute + 90*time.Minute); !rh.BackoffUntil.Equal(want) {
		t.Errorf("BackoffUntil = %v, want %v", rh.BackoffUntil, want)
	}
	recs, err := r.d.store.ListRoleHealth()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || !recs[0].BackoffUntil.Equal(rh.BackoffUntil) {
		t.Errorf("persisted backoff %+v does not match the live one %v", recs, rh.BackoffUntil)
	}
}

// A hook repeated after a crash must not count the same pause twice.
func TestApplyPauseIsIdempotentOnPausedTotal(t *testing.T) {
	r := newTimersRig(t)
	rec := *r.d.suspendRec.Load()
	if err := r.d.applyPause(rec, r.now); err != nil {
		t.Fatal(err)
	}
	r.check(t)
	// A repeat after a crash: the stamps are capped at now, so they may move
	// further; the paused total must not.
	if err := r.d.applyPause(rec, r.now); err != nil {
		t.Fatal(err)
	}
	r.checkPause(t)

	// A later, different suspend adds its own pause.
	rec2 := rec
	rec2.Since = r.now.Add(-30 * time.Minute)
	if err := r.d.applyPause(rec2, r.now); err != nil {
		t.Fatal(err)
	}
	if got := hb(t, r.d, "a").PausedTotal; got != 120*time.Minute {
		t.Errorf("PausedTotal after a second suspend = %v, want 2h", got)
	}
}

// A daemon that starts with a record left suspending or resuming thaws it and
// shifts the timers, which it did not before.
func TestStartThawOfAnInterruptedRecordAppliesThePause(t *testing.T) {
	for _, state := range []suspend.State{suspend.StateSuspending, suspend.StateResuming} {
		t.Run(string(state), func(t *testing.T) {
			r := newTimersRig(t)
			r.d.events = events.NewRing(0)
			rec := *r.d.suspendRec.Load()
			rec.State = state
			if err := (suspendStore{r.d}).Save(rec); err != nil {
				t.Fatal(err)
			}
			r.d.resolveSuspendAtStart()
			r.check(t)
			rh, _ := r.ctrl.RoleHealthSnapshot("ws", "t", "r")
			if want := r.since.Add(4*time.Minute + 90*time.Minute); !rh.BackoffUntil.Equal(want) {
				t.Errorf("BackoffUntil = %v, want %v", rh.BackoffUntil, want)
			}
			if r.d.suspendRec.Load() != nil {
				t.Error("the record was not cleared")
			}
		})
	}
}

// A daemon with no controller yet still shifts the sessions.
func TestApplyPauseWithoutAControllerShiftsSessions(t *testing.T) {
	r := newTimersRig(t)
	r.d.teamCtrl = nil
	rec := *r.d.suspendRec.Load()
	if err := r.d.applyPause(rec, r.now); err != nil {
		t.Fatal(err)
	}
	r.check(t)
}
