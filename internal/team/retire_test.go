package team

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// These tests start no pane: a retirement is driven through the store, and a
// killed session has no pane to remove. So they do not skip where ps cannot
// run.

// retireFixture is a controller on a fixed clock with a recording notifier, a
// fake screen capture into a scratch retired directory (never the real marvel
// home), and one team holding role.
type retireFixture struct {
	t       *testing.T
	store   *api.Store
	ctrl    *Controller
	clock   *testClock
	ring    *events.Ring
	notices []string
	retired string
	dir     string
	teamKey string
}

func newRetireFixture(t *testing.T, ws string, roles ...api.Role) *retireFixture {
	t.Helper()
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	f := &retireFixture{t: t, store: store, ctrl: ctrl, teamKey: ws + "/squad", retired: t.TempDir(), dir: realTempDir(t)}
	f.clock = newTestClock(listEpoch)
	ctrl.now = f.clock.Now
	f.ring = events.NewRing(0)
	ctrl.Events = f.ring
	ctrl.Notify = func(sess api.Session, text, origin string) error {
		f.notices = append(f.notices, sess.Key()+": "+text)
		return nil
	}
	ctrl.CaptureScreen = func(api.Session) (string, error) { return "first line\nlast line\n", nil }
	ctrl.RetiredDir = f.retired
	createTeamFixture(t, store, ws, "squad", roles)
	return f
}

// handoffRole is an interactive role that declares a handoff file in dir.
func handoffRole(dir string) api.Role {
	return api.Role{
		Name: testShiftRole, Replicas: 1,
		Runtime: api.Runtime{Name: "claude", Command: "claude"},
		Shift:   &api.ShiftPolicy{Handoff: filepath.Join(dir, "{session}.md"), HandoffMarker: putMarker},
	}
}

// seat seeds a running seat of role, created an hour ago.
func (f *retireFixture) seat(role string, idx int) api.Session {
	f.t.Helper()
	ws := strings.TrimSuffix(f.teamKey, "/squad")
	s := api.Session{
		Name: "squad-" + role + "-g1-" + string(rune('0'+idx)), Workspace: ws, Team: "squad",
		Role: role, Generation: 1, State: api.SessionRunning, CreatedAt: f.clock.Now().Add(-time.Hour),
	}
	seedSession(f.t, f.store, s)
	return s
}

func (f *retireFixture) team() api.Team {
	f.t.Helper()
	tm, err := f.store.GetTeam(f.teamKey)
	if err != nil {
		f.t.Fatal(err)
	}
	return tm
}

// retire asks sess to leave as scale-down does.
func (f *retireFixture) retire(sess api.Session) {
	f.t.Helper()
	tm := f.team()
	f.ctrl.mu.Lock()
	defer f.ctrl.mu.Unlock()
	f.ctrl.retireSession(&tm, roleOf(&tm, sess.Role), sess, f.clock.Now())
}

// advance runs the retire pass once and waits for the marker reads it began.
func (f *retireFixture) advance() {
	f.t.Helper()
	tm := f.team()
	f.ctrl.mu.Lock()
	f.ctrl.advanceRetirements(&tm, f.clock.Now())
	f.ctrl.mu.Unlock()
	f.ctrl.handoffProbes.wait()
}

func (f *retireFixture) gone(sess api.Session) bool {
	_, err := f.store.GetSession(sess.Key())
	return err != nil
}

func (f *retireFixture) outcomes() []string {
	var out []string
	for _, ev := range f.ring.Snapshot(events.Filter{Kind: events.KindSessionRetired}, 0) {
		out = append(out, ev.Message)
	}
	return out
}

func (f *retireFixture) savedScreens() []string {
	f.t.Helper()
	var out []string
	_ = filepath.Walk(f.retired, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// A retiring seat is sent the notice, with the tail the shift notices use, and
// its pane is killed one tick after the marker is seen: the screen is saved
// 0600 first, then session.retired says handoff-written (section 6.1 to 6.3).
func TestRetireKillsOneTickAfterTheMarker(t *testing.T) {
	f := newRetireFixture(t, "test-retire-marker", handoffRole(realTempDir(t)))
	role := f.team().Roles[0]
	sess := f.seat(testShiftRole, 0)
	f.retire(sess)

	path, _ := api.ExpandHandoffPath(role.Shift.Handoff, sess.Name)
	want := "retire: this seat is being removed; write your handoff now to " + path + ", ending with the line \"" + putMarker + "\"; you have 5m0s"
	if len(f.notices) != 1 || !strings.HasSuffix(f.notices[0], want) {
		t.Fatalf("notices = %q, want one ending %q", f.notices, want)
	}
	if req, ok := f.team().Retiring[sess.Key()]; !ok || req.Path != path || req.Window != api.DefaultShiftHandoffWindow {
		t.Fatalf("Retiring = %+v, want a request for %s at %s", f.team().Retiring, sess.Key(), path)
	}

	f.advance()
	if f.gone(sess) {
		t.Fatal("killed before any marker")
	}
	if err := os.WriteFile(path, []byte("notes\n"+putMarker+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(time.Second)
	f.advance() // this read sees the marker
	if f.gone(sess) {
		t.Fatal("killed on the tick that read the marker; want the tick after")
	}
	f.clock.Advance(time.Second)
	f.advance()
	if !f.gone(sess) {
		t.Fatal("still alive one tick after the marker was seen")
	}
	got := f.outcomes()
	if len(got) != 1 || !strings.Contains(got[0], "outcome=handoff-written") || !strings.Contains(got[0], "screen saved to ") {
		t.Fatalf("session.retired = %q, want handoff-written with the screen saved", got)
	}
	screens := f.savedScreens()
	if len(screens) != 1 || !strings.HasPrefix(screens[0], filepath.Join(f.retired, "test-retire-marker", sess.Name+"-")) {
		t.Fatalf("saved screens = %q, want one under the workspace", screens)
	}
	if info, err := os.Stat(screens[0]); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("screen file mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
	if _, ok := f.team().Retiring[sess.Key()]; ok {
		t.Error("the request outlived the kill")
	}
}

// With no marker by the end of the window, the pane is killed and the outcome
// is handoff-missing (section 6.2).
func TestRetireKillsAtTheWindowWithNoMarker(t *testing.T) {
	f := newRetireFixture(t, "test-retire-window", handoffRole(realTempDir(t)))
	sess := f.seat(testShiftRole, 0)
	f.retire(sess)

	f.clock.Advance(api.DefaultShiftHandoffWindow - time.Second)
	f.advance()
	if f.gone(sess) {
		t.Fatal("killed inside the window")
	}
	f.clock.Advance(2 * time.Second)
	f.advance() // a read begun after the deadline
	f.clock.Advance(time.Second)
	f.advance()
	if !f.gone(sess) {
		t.Fatal("alive past the window")
	}
	if got := f.outcomes(); len(got) != 1 || !strings.Contains(got[0], "outcome=handoff-missing") || !strings.Contains(got[0], "window 5m0s passed") {
		t.Fatalf("session.retired = %q, want handoff-missing at the window", got)
	}
}

// A role that declares no handoff path gets the notice with no path and the
// default window as a plain grace (the step 4 ruling, Q4).
func TestRetireWithNoPathIsAPlainGrace(t *testing.T) {
	f := newRetireFixture(t, "test-retire-grace", sleepRole(testShiftRole, 1))
	sess := f.seat(testShiftRole, 0)
	f.retire(sess)

	if len(f.notices) != 1 || !strings.HasSuffix(f.notices[0], "retire: this seat is being removed; write your handoff now; you have 5m0s") {
		t.Fatalf("notices = %q, want the plain-grace notice", f.notices)
	}
	f.clock.Advance(api.DefaultShiftHandoffWindow - time.Second)
	f.advance()
	if f.gone(sess) {
		t.Fatal("killed inside the grace")
	}
	f.clock.Advance(time.Second)
	f.advance()
	if !f.gone(sess) {
		t.Fatal("alive at the end of the grace")
	}
	if got := f.outcomes(); len(got) != 1 || !strings.Contains(got[0], "plain grace") {
		t.Fatalf("session.retired = %q, want handoff-missing naming the plain grace", got)
	}
}

// A seat that is not running is deleted at once, not asked: a pending seat
// beside a finished one (section 6.2's "not waited for" list; the director's
// acceptance item).
func TestRetireDeletesAPendingSeatAtOnce(t *testing.T) {
	f := newRetireFixture(t, "test-retire-pending", handoffRole(realTempDir(t)))
	for _, state := range []api.SessionState{api.SessionPending, api.SessionSucceeded} {
		sess := f.seat(testShiftRole, len(f.outcomes()))
		if err := f.store.UpdateSession(sess.Key(), func(live *api.Session) error {
			live.State = state
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		sess.State = state
		f.retire(sess)
		if !f.gone(sess) {
			t.Fatalf("%s seat not deleted at once", state)
		}
	}
	if len(f.notices) != 0 {
		t.Errorf("notices = %q, want none", f.notices)
	}
	got := f.outcomes()
	if len(got) != 2 || !strings.Contains(got[0], "outcome=not-asked: pending") || !strings.Contains(got[1], "outcome=not-asked: not running") {
		t.Errorf("session.retired = %q, want not-asked pending, then not running", got)
	}
}

// A retirement restored from the store resumes from its RequestedAt under a
// new controller; one whose pane did not survive the restart ends not-asked,
// "pane gone" (the step 4 ruling, Q11).
func TestRetireResumesAfterARestart(t *testing.T) {
	f := newRetireFixture(t, "test-retire-restart", sleepRole(testShiftRole, 2))
	kept, lost := f.seat(testShiftRole, 0), f.seat(testShiftRole, 1)
	f.retire(kept)
	f.retire(lost)
	if err := f.store.UpdateSession(lost.Key(), func(live *api.Session) error {
		live.State = api.SessionCrashed
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	restarted := NewController(f.store, f.ctrl.sessMgr)
	restarted.now, restarted.Events = f.clock.Now, f.ring
	f.ctrl = restarted
	f.advance()
	if !f.gone(lost) {
		t.Fatal("the seat whose pane did not survive is still stored")
	}
	if got := f.outcomes(); len(got) != 1 || !strings.Contains(got[0], "outcome=not-asked: pane gone") {
		t.Fatalf("session.retired = %q, want not-asked pane gone", got)
	}
	if f.gone(kept) {
		t.Fatal("the surviving seat was killed inside its window")
	}
	f.clock.Advance(api.DefaultShiftHandoffWindow)
	f.advance()
	if !f.gone(kept) {
		t.Fatal("the resumed retirement did not end at its window")
	}
}

// A retiring seat is not counted as actual, so the role plans a fresh seat for
// it, and scale-down never picks it again (section 6.1).
func TestRetiringSeatIsNotCountedAsActual(t *testing.T) {
	f := newRetireFixture(t, "test-retire-actual", sleepRole(testShiftRole, 1))
	sess := f.seat(testShiftRole, 0)
	f.retire(sess)
	tm := f.team()
	f.ctrl.mu.Lock()
	plan := f.ctrl.planRole(&tm, &tm.Roles[0], tm.Generation)
	f.ctrl.mu.Unlock()
	if plan.Actual != 0 || plan.Spawn != 1 {
		t.Fatalf("plan actual %d spawn %d, want 0 and 1", plan.Actual, plan.Spawn)
	}
	if n := f.ctrl.nextIndex(&tm, &tm.Roles[0], tm.Generation); n != 1 {
		t.Errorf("next index = %d, want 1: the retiring seat keeps its name", n)
	}
}

// A request is dropped when the session under its key was created at or after
// the ask: that session was never asked (the step 4 ruling, Q1).
func TestRetireDropsARequestForANewerSession(t *testing.T) {
	f := newRetireFixture(t, "test-retire-newer", sleepRole(testShiftRole, 1))
	sess := f.seat(testShiftRole, 0)
	f.retire(sess)
	if err := f.store.UpdateSession(sess.Key(), func(live *api.Session) error {
		live.CreatedAt = f.clock.Now()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.advance()
	if _, ok := f.team().Retiring[sess.Key()]; ok || f.gone(sess) {
		t.Fatalf("Retiring %+v, gone %v; want the request dropped and the newer session left alone", f.team().Retiring, f.gone(sess))
	}
}

// A retiring seat that dies on its own ends handoff-missing, "seat ended",
// and is not restarted (the step 4 ruling, Q12).
func TestRetireRecordsASeatThatEnds(t *testing.T) {
	f := newRetireFixture(t, "test-retire-ended", sleepRole(testShiftRole, 1))
	sess := f.seat(testShiftRole, 0)
	f.retire(sess)
	if err := f.store.UpdateSession(sess.Key(), func(live *api.Session) error {
		live.State = api.SessionCrashed
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.advance()
	if got := f.outcomes(); len(got) != 1 || !strings.Contains(got[0], "outcome=handoff-missing: seat ended") {
		t.Fatalf("session.retired = %q, want handoff-missing seat ended", got)
	}
	if !f.gone(sess) {
		t.Error("the ended seat is still stored")
	}
}

// Retiring a seat drops the shift request it held, and the shift triggers
// skip a retiring seat (the step 4 ruling, Q12).
func TestRetireEndsTheSeatsShiftRequestAndTriggersSkipIt(t *testing.T) {
	dir := realTempDir(t)
	f := newRetireFixture(t, "test-retire-shift", maxAgeRole(dir))
	sess := f.seat(testShiftRole, 0)
	if err := f.store.UpdateSession(sess.Key(), func(live *api.Session) error {
		live.CreatedAt = f.clock.Now().Add(-testMaxAge - time.Hour)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	tm := f.team()
	cond := tm.Roles[0].Shift.Any[0]
	f.ctrl.mu.Lock()
	f.ctrl.requestHandoff(&tm, &tm.Roles[0], cond, sess, handoffAsk{notice: handoffNotice, origin: NoticeMaxAge, detail: "test"}, f.clock.Now())
	f.ctrl.mu.Unlock()
	if _, ok := f.team().ShiftRequests[testShiftRole]; !ok {
		t.Fatal("setup: no shift request")
	}

	f.retire(sess)
	if _, ok := f.team().ShiftRequests[testShiftRole]; ok {
		t.Fatal("the seat's shift request outlived its retirement")
	}
	before := len(f.notices)
	tm = f.team()
	f.ctrl.mu.Lock()
	f.ctrl.autoShiftsThisTick = 0
	f.ctrl.evaluateShiftTriggers(&tm)
	f.ctrl.mu.Unlock()
	if len(f.notices) != before || len(f.team().ShiftRequests) != 0 {
		t.Errorf("notices %q, requests %+v; want no trigger on a retiring seat", f.notices[before:], f.team().ShiftRequests)
	}
}

// A role dropped from the manifest is retired with the plain grace, and its
// role.removed, health and holds wait until its last seat has gone (the step 4
// ruling, Q5).
func TestDroppedRoleIsRemovedOnceItsLastSeatLeaves(t *testing.T) {
	f := newRetireFixture(t, "test-retire-drop", sleepRole(testShiftRole, 1), sleepRole("helper", 1))
	sess := f.seat("helper", 0)
	roleKey := "test-retire-drop/squad/helper"
	f.ctrl.roleHealth[roleKey] = &RoleHealth{RestartCount: 2}
	if err := f.store.UpdateTeam(f.teamKey, func(live *api.Team) error {
		live.Roles = live.Roles[:1]
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	tm := f.team()
	f.ctrl.mu.Lock()
	f.ctrl.reconcileOrphanedSessions(&tm)
	f.ctrl.mu.Unlock()
	if len(f.notices) != 1 || !strings.HasSuffix(f.notices[0], "write your handoff now; you have 5m0s") {
		t.Fatalf("notices = %q, want the plain-grace notice", f.notices)
	}
	if n := len(f.ring.Snapshot(events.Filter{Kind: events.KindRoleRemoved}, 0)); n != 0 || f.ctrl.roleHealth[roleKey] == nil {
		t.Fatalf("role.removed = %d, health %+v; want none until the seat leaves", n, f.ctrl.roleHealth[roleKey])
	}
	f.clock.Advance(api.DefaultShiftHandoffWindow)
	f.advance()
	if !f.gone(sess) {
		t.Fatal("the dropped role's seat outlived its grace")
	}
	if n := len(f.ring.Snapshot(events.Filter{Kind: events.KindRoleRemoved}, 0)); n != 1 || f.ctrl.roleHealth[roleKey] != nil {
		t.Errorf("role.removed = %d, health %+v; want one event and the health gone", n, f.ctrl.roleHealth[roleKey])
	}
}

// A running headless seat keeps today's delete: no notice, no wait, nothing
// recorded as a retirement, because the headless arm awaits the operator's
// answer (the step 4 ruling, Q7).
func TestRetireLeavesAHeadlessSeatToTodaysDelete(t *testing.T) {
	role := sleepRole(testShiftRole, 1)
	role.Runtime.Mode = api.RuntimeModeHeadless
	f := newRetireFixture(t, "test-retire-headless", role)
	sess := f.seat(testShiftRole, 0)
	f.retire(sess)
	if !f.gone(sess) || len(f.notices) != 0 || len(f.team().Retiring) != 0 || len(f.outcomes()) != 0 {
		t.Errorf("gone %v, notices %q, retiring %+v, events %q; want a plain delete", f.gone(sess), f.notices, f.team().Retiring, f.outcomes())
	}
}

// A retiring seat answers through handoff.put exactly as a seat holding a shift
// request does, and its retirement ends handoff-written (the step 4 ruling,
// Q1).
func TestRetiringSeatHandsOffThroughThePut(t *testing.T) {
	f := newRetireFixture(t, "test-retire-put", handoffRole(realTempDir(t)))
	tok, hash, err := api.NewHeartbeatToken()
	if err != nil {
		t.Fatal(err)
	}
	sess := f.seat(testShiftRole, 0)
	if err := f.store.UpdateSession(sess.Key(), func(live *api.Session) error {
		live.HeartbeatToken, live.HeartbeatTokenHash = tok, hash
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sess, _ = f.store.GetSession(sess.Key())
	f.retire(sess)

	put, err := f.ctrl.PutHandoff(tok, "what I was doing")
	if err != nil {
		t.Fatalf("handoff.put from a retiring seat: %v", err)
	}
	body, err := os.ReadFile(put.Path)
	if err != nil || !strings.HasSuffix(string(body), putMarker+"\n") {
		t.Fatalf("handoff file %q: %v; want it to end in the marker", body, err)
	}
	if f.team().Retiring[sess.Key()].PutAt.IsZero() {
		t.Error("the put was not recorded on the retirement")
	}
	f.advance()
	f.clock.Advance(time.Second)
	f.advance()
	if got := f.outcomes(); len(got) != 1 || !strings.Contains(got[0], "outcome=handoff-written") {
		t.Fatalf("session.retired = %q, want handoff-written", got)
	}
}

// A marker written just before the deadline is honoured: a read begun before
// the deadline cannot show it is absent, so the kill waits for a read begun
// after it, which finds the marker (section 6.2).
func TestRetireWaitsForAReadBegunAfterTheDeadline(t *testing.T) {
	f := newRetireFixture(t, "test-retire-late", handoffRole(realTempDir(t)))
	role := f.team().Roles[0]
	sess := f.seat(testShiftRole, 0)
	f.retire(sess)
	f.clock.Advance(api.DefaultShiftHandoffWindow - time.Second)
	f.advance() // a read before the deadline finds nothing
	path, _ := api.ExpandHandoffPath(role.Shift.Handoff, sess.Name)
	if err := os.WriteFile(path, []byte(putMarker+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(2 * time.Second)
	f.advance() // past the deadline, the last finished read began before it
	if f.gone(sess) {
		t.Fatal("killed on a read begun before the deadline")
	}
	f.clock.Advance(time.Second)
	f.advance()
	if got := f.outcomes(); len(got) != 1 || !strings.Contains(got[0], "outcome=handoff-written") {
		t.Fatalf("session.retired = %q, want handoff-written", got)
	}
}

// The health path does not restart a retiring seat, however stale its
// heartbeat: its retirement records how it ends (the step 4 ruling, Q12).
func TestHealthLeavesARetiringSeatAlone(t *testing.T) {
	role := sleepRole(testShiftRole, 1)
	role.RestartPolicy = api.RestartAlways
	role.HealthCheck = &api.HealthCheck{Type: api.HealthCheckHeartbeat, Timeout: time.Millisecond, FailureThreshold: 1}
	f := newRetireFixture(t, "test-retire-health", role)
	sess := f.seat(testShiftRole, 0)
	if err := f.store.UpdateSession(sess.Key(), func(live *api.Session) error {
		live.LastHeartbeat = time.Now().UTC().Add(-time.Hour)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.retire(sess)
	f.ctrl.mu.Lock()
	f.ctrl.evaluateHealth()
	f.ctrl.evaluateHealth()
	f.ctrl.mu.Unlock()
	got, err := f.store.GetSession(sess.Key())
	if err != nil || got.State != api.SessionRunning || got.FailureCount != 0 {
		t.Fatalf("session %+v, %v; want it running, uncounted and not restarted", got.State, err)
	}
}
