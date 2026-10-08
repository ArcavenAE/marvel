package team

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// ringFixture is a seat on a fixed clock whose tick ring holds span of two-second
// ticks ending at now, each judged against a ContextAt exactly age older than
// the tick, so the seat is "seen age ago" at every tick.
type ringFixture struct {
	*listFixture
	sess api.Session
	now  time.Time
}

func newRingFixture(t *testing.T, ws string, role api.Role, age, span time.Duration) *ringFixture {
	t.Helper()
	f := newListFixture(t, ws, role)
	sess := f.seed(time.Hour, time.Minute, 0, 0)
	if err := f.store.UpdateSession(sess.Key(), func(live *api.Session) error {
		live.Runtime.Mode = api.RuntimeModeHeadless
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	for off := -span; off <= 0; off += 2 * time.Second {
		at := now.Add(off)
		f.ctrl.recordTick(sess.Key(), at, at.Add(-age))
	}
	got, err := f.store.GetSession(sess.Key())
	if err != nil {
		t.Fatal(err)
	}
	return &ringFixture{listFixture: f, sess: got, now: now}
}

func workerRole(timeout time.Duration) api.Role {
	return api.Role{
		Name: testShiftRole, Replicas: 1, ActivityTimeout: timeout,
		Runtime: api.Runtime{Name: "claude", Command: "claude"},
	}
}

// A role that declares an activity_timeout is judged by it: a seat last seen
// three minutes before every tick is quiet under two.
func TestActivePctUsesRoleTimeoutWhenSet(t *testing.T) {
	f := newRingFixture(t, "test-ring-role", workerRole(2*time.Minute), 3*time.Minute, TickRingWindow)
	got := f.ctrl.ActivityOf(f.sess, f.now)
	if got.Window != 2*time.Minute || got.Active != 0 || got.Total != 450 {
		t.Fatalf("reading = %+v, want window 2m, 0 of 450 active", got)
	}
}

// With no timeout on the role and no operator window, the default quiet window
// applies: the same seat, last seen three minutes before each tick, is active.
func TestActivePctUsesDefaultWindowWhenUnset(t *testing.T) {
	f := newRingFixture(t, "test-ring-default", workerRole(0), 3*time.Minute, TickRingWindow)
	got := f.ctrl.ActivityOf(f.sess, f.now)
	if got.Window != api.DefaultQuietWindow || got.Active != 450 || got.Total != 450 {
		t.Fatalf("reading = %+v, want the default window, 450 of 450 active", got)
	}
}

// The operator's watchdog.window moves ACTIVE% with the watchdog, between the
// role's own timeout and the default.
func TestActivePctUsesTheClusterWindowWhenTheRoleDeclaresNone(t *testing.T) {
	f := newRingFixture(t, "test-ring-cluster", workerRole(0), 3*time.Minute, TickRingWindow)
	f.ctrl.SetClusterQuietWindow(time.Minute)
	got := f.ctrl.ActivityOf(f.sess, f.now)
	if got.Window != time.Minute || got.Active != 0 {
		t.Fatalf("reading = %+v, want window 1m and 0 active", got)
	}

	withRole := newRingFixture(t, "test-ring-cluster-role", workerRole(5*time.Minute), 3*time.Minute, TickRingWindow)
	withRole.ctrl.SetClusterQuietWindow(time.Minute)
	if got := withRole.ctrl.ActivityOf(withRole.sess, withRole.now); got.Window != 5*time.Minute || got.Active != 450 {
		t.Fatalf("the role's own timeout lost to the cluster window: %+v", got)
	}
}

// The ring is full only once it has watched the whole window, and At is the
// newest tick.
func TestActivityOfIsFullOnlyAfterTheWindow(t *testing.T) {
	short := newRingFixture(t, "test-ring-short", workerRole(0), time.Second, 14*time.Minute)
	if got := short.ctrl.ActivityOf(short.sess, short.now); got.Full {
		t.Errorf("full after 14 minutes: %+v", got)
	}
	long := newRingFixture(t, "test-ring-long", workerRole(0), time.Second, TickRingWindow)
	got := long.ctrl.ActivityOf(long.sess, long.now)
	if !got.Full || !got.At.Equal(long.now) {
		t.Errorf("reading = %+v, want full with At the newest tick %v", got, long.now)
	}
}

// A seat marvel has no activity channel for is not observable, and a session
// with an empty ring reads no ticks and is not full.
func TestActivityOfObservableFollowsTheActivityChannel(t *testing.T) {
	f := newRingFixture(t, "test-ring-chan", workerRole(0), time.Second, time.Minute)
	if got := f.ctrl.ActivityOf(f.sess, f.now); !got.Observable {
		t.Errorf("a headless seat should be observable: %+v", got)
	}
	if err := f.store.UpdateSession(f.sess.Key(), func(live *api.Session) error {
		live.Runtime.Mode = ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	bare, err := f.store.GetSession(f.sess.Key())
	if err != nil {
		t.Fatal(err)
	}
	if got := f.ctrl.ActivityOf(bare, f.now); got.Observable {
		t.Errorf("an interactive seat with no feed should not be observable: %+v", got)
	}
	empty := f.ctrl.ActivityOf(api.Session{Name: "nobody", Workspace: "ws"}, f.now)
	if empty.Total != 0 || empty.Full {
		t.Errorf("a session never evaluated = %+v, want no ticks and not full", empty)
	}
}
