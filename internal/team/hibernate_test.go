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

// These tests seed running seats in the store and drive the sign-off through
// the controller on a fixed clock. No pane is started and nothing signals a
// process, so they run beside the retire tests on the same fixture.

func hibernateRole(name, dir string, replicas int) api.Role {
	r := handoffRole(dir)
	r.Name, r.Replicas = name, replicas
	return r
}

func (f *retireFixture) hibernate(opt HibernateOptions) HibernateReport {
	f.t.Helper()
	rep, err := f.ctrl.Hibernate(f.teamKey, opt)
	if err != nil {
		f.t.Fatalf("Hibernate: %v", err)
	}
	return rep
}

// tick runs the hibernate pass once and waits for the marker reads it began.
func (f *retireFixture) tick() {
	f.t.Helper()
	tm := f.team()
	f.ctrl.mu.Lock()
	f.ctrl.advanceHibernate(&tm, f.clock.Now())
	f.ctrl.mu.Unlock()
	f.ctrl.handoffProbes.wait()
}

func (f *retireFixture) writeMarker(sess api.Session, role api.Role) {
	f.t.Helper()
	path, _ := api.ExpandHandoffPath(role.Shift.Handoff, sess.Name)
	if err := os.WriteFile(path, []byte("notes\n"+putMarker+"\n"), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *retireFixture) eventsOf(k events.Kind) []string {
	var out []string
	for _, ev := range f.ring.Snapshot(events.Filter{Kind: k}, 0) {
		out = append(out, ev.Message)
	}
	return out
}

func TestHibernateAsksEveryReplicaInOneTick(t *testing.T) {
	f := newRetireFixture(t, "test-hib-two", hibernateRole("worker", realTempDir(t), 2))
	a, b := f.seat("worker", 0), f.seat("worker", 1)

	f.hibernate(HibernateOptions{Actor: "op", Source: "cli"})
	f.tick()

	if len(f.notices) != 2 {
		t.Fatalf("notices = %q, want one per replica", f.notices)
	}
	reqs := f.team().HibernateRequests
	if len(reqs) != 2 || reqs[a.Key()].Cause != api.HandoffCauseHibernate || reqs[b.Key()].Session != b.Key() {
		t.Fatalf("HibernateRequests = %+v, want one per seat with cause hibernate", reqs)
	}
	if len(f.team().ShiftRequests) != 0 {
		t.Fatalf("ShiftRequests = %+v, want none: a hibernate never uses them", f.team().ShiftRequests)
	}
	f.tick()
	if len(f.notices) != 2 {
		t.Fatalf("a second tick re-asked: notices = %q", f.notices)
	}
}

func TestHibernateRequestsSurviveARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.db")
	store, ctrl, cleanup := setupWithBolt(t, path)
	f := newRetireFixtureOn(t, store, ctrl, "test-hib-restart", hibernateRole("worker", realTempDir(t), 2))
	f.seat("worker", 0)
	f.seat("worker", 1)
	f.hibernate(HibernateOptions{Actor: "op", Source: "cli"})
	f.tick()
	cleanup()

	reopened := api.NewStore()
	if err := reopened.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.CloseBolt() }()
	tm, err := reopened.GetTeam(f.teamKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(tm.HibernateRequests) != 2 || tm.Hibernate == nil || tm.Hibernate.State != api.HibernateHibernated {
		t.Fatalf("after a restart: mark %+v, requests %+v; want the mark and both requests", tm.Hibernate, tm.HibernateRequests)
	}
}

func TestHibernateClearsAnOpenMaxAgeRequest(t *testing.T) {
	f := newRetireFixture(t, "test-hib-super", hibernateRole("worker", realTempDir(t), 1))
	sess := f.seat("worker", 0)
	if err := f.store.UpdateTeam(f.teamKey, func(live *api.Team) error {
		live.ShiftRequests = map[string]api.ShiftRequest{"worker": {Session: sess.Key(), Cause: api.ShiftTriggerMaxAge, RequestedAt: f.clock.Now()}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	f.hibernate(HibernateOptions{Actor: "op", Source: "cli"})

	if got := f.team().ShiftRequests; len(got) != 0 {
		t.Fatalf("ShiftRequests = %+v, want the max-age request superseded", got)
	}
}

func TestShiftTriggersSkipAHibernatedRole(t *testing.T) {
	role := hibernateRole("worker", realTempDir(t), 1)
	role.Shift.Any = []api.ShiftCondition{{On: api.ShiftTriggerMaxAge, MaxAge: time.Minute}}
	f := newRetireFixture(t, "test-hib-skip", role)
	f.seat("worker", 0)
	f.hibernate(HibernateOptions{Actor: "op", Source: "cli"})

	tm := f.team()
	f.ctrl.mu.Lock()
	f.ctrl.evaluateShiftTriggers(&tm)
	f.ctrl.mu.Unlock()
	f.ctrl.handoffProbes.wait()

	if len(f.team().ShiftRequests) != 0 || len(f.notices) != 0 {
		t.Fatalf("a hibernated role got a shift request: %+v, notices %q", f.team().ShiftRequests, f.notices)
	}
}

// The seat is ended on its marker and no successor starts; the mark keeps the
// slot empty, even under a converge posture, so the drain never reads as a
// crash that needs repairing.
func TestHibernateEndsTheSeatOnItsMarkerAndStartsNoSuccessor(t *testing.T) {
	role := hibernateRole("worker", realTempDir(t), 1)
	f := newRetireFixture(t, "test-hib-end", role)
	if err := f.store.UpdateTeam(f.teamKey, func(live *api.Team) error {
		live.ConvergencePosture = api.PostureConverge
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sess := f.seat("worker", 0)
	f.hibernate(HibernateOptions{Actor: "op", Source: "cli"})
	f.tick()
	f.writeMarker(sess, role)
	f.clock.Advance(time.Second)
	f.tick() // reads the marker
	f.clock.Advance(time.Second)
	f.tick()

	if !f.gone(sess) {
		t.Fatal("the seat survived its marker")
	}
	if left := f.store.ListSessionsByTeam("test-hib-end", "squad"); len(left) != 0 {
		t.Fatalf("sessions after the end = %+v, want none: no successor", left)
	}
	if got := f.outcomes(); len(got) != 1 || !strings.Contains(got[0], "outcome=handoff-written") {
		t.Fatalf("session.retired = %q, want handoff-written", got)
	}
	if said := f.eventsOf(events.KindTeamHibernated); len(said) != 1 {
		t.Fatalf("team.hibernated = %q, want one", said)
	}
	tm := f.team()
	f.ctrl.mu.Lock()
	plan := f.ctrl.planRole(&tm, &tm.Roles[0], tm.Generation)
	f.ctrl.mu.Unlock()
	if plan.Action != RoleHold || plan.Hold != HoldHibernated || plan.Spawn != 0 {
		t.Fatalf("plan under the mark = %+v, want a hibernated hold and no spawn", plan)
	}
}

// A seat with no marker is never ended: it is reported, once per window, with
// cause hibernate, and the hibernate waits for it.
func TestHibernateNeverEndsASeatThatWroteNoHandoff(t *testing.T) {
	f := newRetireFixture(t, "test-hib-missing", hibernateRole("worker", realTempDir(t), 1))
	sess := f.seat("worker", 0)
	f.hibernate(HibernateOptions{Actor: "op", Source: "cli"})
	f.tick()
	window := api.DefaultShiftHandoffWindow

	f.clock.Advance(window + time.Second)
	f.tick() // starts the read that can show the marker absent
	f.clock.Advance(time.Second)
	f.tick()

	if f.gone(sess) {
		t.Fatal("a seat with no marker was ended")
	}
	if req := f.team().HibernateRequests[sess.Key()]; !req.Escalated {
		t.Fatalf("request = %+v, want it escalated", req)
	}
	got := f.eventsOf(events.KindShiftHandoffMissing)
	if len(got) != 1 || !strings.Contains(got[0], "cause=hibernate") || !strings.Contains(got[0], "hibernate --end") {
		t.Fatalf("team.shift-handoff-missing = %q, want one naming cause=hibernate and --end", got)
	}
	f.clock.Advance(window)
	f.tick()
	f.clock.Advance(time.Second)
	f.tick()
	if got := f.eventsOf(events.KindShiftHandoffMissing); len(got) != 2 {
		t.Fatalf("after another window: %d events, want the repeat (2)", len(got))
	}
	if said := f.eventsOf(events.KindTeamHibernated); len(said) != 0 {
		t.Fatalf("team.hibernated = %q before the seat ended", said)
	}
}

func TestHibernateAsksSupervisorsAfterTheWorkers(t *testing.T) {
	dir := realTempDir(t)
	worker, sup := hibernateRole("worker", dir, 1), hibernateRole("supervisor", dir, 1)
	f := newRetireFixture(t, "test-hib-order", worker, sup)
	w, s := f.seat("worker", 0), f.seat("supervisor", 0)

	f.hibernate(HibernateOptions{Actor: "op", Source: "cli"})
	f.tick()
	if len(f.notices) != 1 || !strings.HasPrefix(f.notices[0], w.Key()) {
		t.Fatalf("first tick notices = %q, want the worker only", f.notices)
	}
	f.writeMarker(w, worker)
	f.clock.Advance(time.Second)
	f.tick()
	f.clock.Advance(time.Second)
	f.tick() // the worker ends
	f.tick()
	if len(f.notices) != 2 || !strings.HasPrefix(f.notices[1], s.Key()) {
		t.Fatalf("notices = %q, want the supervisor asked once the worker ended", f.notices)
	}
}

func TestHibernateReportsAWorkerToTheSupervisorsNotice(t *testing.T) {
	dir := realTempDir(t)
	f := newRetireFixture(t, "test-hib-report", hibernateRole("worker", dir, 1), hibernateRole("supervisor", dir, 1))
	w := f.seat("worker", 0)
	f.seat("supervisor", 0)
	f.hibernate(HibernateOptions{Actor: "op", Source: "cli"})
	f.tick()
	f.clock.Advance(api.DefaultShiftHandoffWindow + time.Second)
	f.tick()
	f.clock.Advance(time.Second)
	f.tick() // the worker is reported
	f.tick() // the supervisor is asked

	if len(f.notices) != 2 || !strings.Contains(f.notices[1], "still running with no handoff: "+w.Key()) {
		t.Fatalf("notices = %q, want the supervisor's notice to list the reported worker", f.notices)
	}
}

func TestHibernateRefusesAMidShiftTeamAndABadRole(t *testing.T) {
	f := newRetireFixture(t, "test-hib-refuse", hibernateRole("worker", realTempDir(t), 1))
	if _, err := f.ctrl.Hibernate(f.teamKey, HibernateOptions{Roles: []string{"nope"}}); err == nil {
		t.Fatal("a role the team does not have was accepted")
	}
	if err := f.store.UpdateTeam(f.teamKey, func(live *api.Team) error {
		live.Shift.Phase = api.ShiftLaunching
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ctrl.Hibernate(f.teamKey, HibernateOptions{}); err == nil || !strings.Contains(err.Error(), "mid-shift") {
		t.Fatalf("a mid-shift team was hibernated: err = %v", err)
	}
	if f.team().Hibernate != nil {
		t.Fatal("a refused hibernate left a mark")
	}
}

func TestHibernateEndEndsAnUnwrittenSeatByName(t *testing.T) {
	f := newRetireFixture(t, "test-hib-endname", hibernateRole("worker", realTempDir(t), 2))
	a, b := f.seat("worker", 0), f.seat("worker", 1)
	if err := f.ctrl.HibernateEnd(f.teamKey, a.Key()); err == nil {
		t.Fatal("--end worked on a team that is not hibernating")
	}
	f.hibernate(HibernateOptions{Actor: "op", Source: "cli"})
	f.tick()

	if err := f.ctrl.HibernateEnd(f.teamKey, a.Key()); err != nil {
		t.Fatal(err)
	}
	if !f.gone(a) || f.gone(b) {
		t.Fatal("--end must end exactly the named seat")
	}
	if _, ok := f.team().HibernateRequests[a.Key()]; ok {
		t.Fatal("--end left the seat's request open")
	}
	if got := f.outcomes(); len(got) != 1 || !strings.Contains(got[0], "outcome=handoff-missing") {
		t.Fatalf("session.retired = %q, want one handoff-missing", got)
	}
}

func TestSubsetHibernateAddsRolesAndAWholeTeamEmptiesThem(t *testing.T) {
	dir := realTempDir(t)
	f := newRetireFixture(t, "test-hib-subset", hibernateRole("a", dir, 1), hibernateRole("b", dir, 1), hibernateRole("c", dir, 1))
	f.hibernate(HibernateOptions{Actor: "op", Source: "cli", Roles: []string{"a"}})
	f.hibernate(HibernateOptions{Actor: "op", Source: "cli", Roles: []string{"b"}})
	if got := f.team().Hibernate.Roles; strings.Join(got, ",") != "a,b" {
		t.Fatalf("roles = %v, want a and b", got)
	}
	f.hibernate(HibernateOptions{Actor: "op", Source: "cli"})
	if got := f.team().Hibernate.Roles; len(got) != 0 {
		t.Fatalf("roles after a whole-team hibernate = %v, want empty (all)", got)
	}
}

// Seats only grows: a second subset hibernate keeps the seats the first ended,
// and so does a hibernate that starts over a waking mark.
func TestSecondHibernateKeepsTheSeatsTheFirstEnded(t *testing.T) {
	dir := realTempDir(t)
	f := newRetireFixture(t, "test-hib-seats", hibernateRole("a", dir, 1), hibernateRole("b", dir, 1))
	f.hibernate(HibernateOptions{Actor: "op", Source: "cli", Roles: []string{"a"}})
	tm := f.team()
	f.ctrl.mu.Lock()
	f.ctrl.recordHibernatedSeat(&tm, "test-hib-seats/squad-a-g1-0")
	f.ctrl.mu.Unlock()
	f.hibernate(HibernateOptions{Actor: "op", Source: "cli", Roles: []string{"b"}})
	if got := f.team().Hibernate.Seats; len(got) != 1 || got[0] != "test-hib-seats/squad-a-g1-0" {
		t.Fatalf("seats after a second subset hibernate = %v, want the first one kept", got)
	}
	if err := f.store.UpdateTeam(f.teamKey, func(live *api.Team) error {
		live.Hibernate.State = api.HibernateWaking
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.hibernate(HibernateOptions{Actor: "op", Source: "cli", Roles: []string{"b"}})
	m := f.team().Hibernate
	if m.State != api.HibernateHibernated || len(m.Seats) != 1 {
		t.Fatalf("mark over a waking one = %+v, want hibernated with the seat kept", m)
	}
}

// The by-role shift loops must never read the per-session hibernate map: its
// keys are session keys, and a loop that took them for role names would act on
// the wrong thing.
func TestOnlyHibernateCodeReadsHibernateRequests(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") || file == "hibernate.go" {
			continue
		}
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "HibernateRequests") {
			t.Errorf("%s reads HibernateRequests; only hibernate.go may", file)
		}
	}
}

// A read-only seat answers its hibernate notice through the daemon: the put
// writes the file the notice named, and the seat is then ended on its marker.
func TestHibernatingSeatHandsOffThroughThePut(t *testing.T) {
	f := newRetireFixture(t, "test-hib-put", hibernateRole("worker", realTempDir(t), 1))
	tok, hash, err := api.NewHeartbeatToken()
	if err != nil {
		t.Fatal(err)
	}
	sess := f.seat("worker", 0)
	if err := f.store.UpdateSession(sess.Key(), func(live *api.Session) error {
		live.HeartbeatToken, live.HeartbeatTokenHash = tok, hash
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ctrl.PutHandoff(tok, "early"); err == nil {
		t.Fatal("a put before any request was accepted")
	}
	f.hibernate(HibernateOptions{Actor: "op", Source: "cli"})
	f.tick()

	put, err := f.ctrl.PutHandoff(tok, "what I was doing")
	if err != nil {
		t.Fatalf("handoff.put from a hibernating seat: %v", err)
	}
	body, err := os.ReadFile(put.Path)
	if err != nil || !strings.HasSuffix(string(body), putMarker+"\n") {
		t.Fatalf("handoff file %q: %v; want it to end in the marker", body, err)
	}
	if f.team().HibernateRequests[sess.Key()].PutAt.IsZero() {
		t.Error("the put was not recorded on the request")
	}
	f.clock.Advance(time.Second)
	f.tick()
	f.clock.Advance(time.Second)
	f.tick()
	if !f.gone(sess) {
		t.Fatal("the seat survived the handoff it put")
	}
}

// A shift launches its successors outside the planner, so the mark has to stop
// it where it starts: an operator shift of a hibernated role is refused, and a
// role the mark does not cover can still shift.
func TestShiftIsRefusedForAHibernatedRole(t *testing.T) {
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	rt := api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}
	createTeamFixture(t, store, "test-hib-shift", "squad", []api.Role{
		{Name: "a", Replicas: 1, Runtime: rt}, {Name: "b", Replicas: 1, Runtime: rt},
	})
	if err := store.UpdateTeam("test-hib-shift/squad", func(live *api.Team) error {
		live.Hibernate = &api.HibernateMark{State: api.HibernateHibernated, Since: time.Now().UTC(), Roles: []string{"a"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.InitiateShift("test-hib-shift/squad", "a"); err == nil || !strings.Contains(err.Error(), "hibernated") {
		t.Fatalf("shift of a hibernated role: err = %v, want a refusal naming the hibernate", err)
	}
	if err := ctrl.InitiateShift("test-hib-shift/squad", ""); err == nil || !strings.Contains(err.Error(), "hibernated") {
		t.Fatalf("whole-team shift over a hibernated role: err = %v, want a refusal", err)
	}
	if err := ctrl.InitiateShift("test-hib-shift/squad", "b"); err != nil {
		t.Fatalf("shift of an uncovered role: %v", err)
	}
}

func TestHibernateNoticeSaysTheSeatReturnsAsASuccessor(t *testing.T) {
	f := newRetireFixture(t, "test-hib-notice", hibernateRole("worker", realTempDir(t), 1))
	f.seat("worker", 0)

	f.hibernate(HibernateOptions{Actor: "op", Source: "cli"})
	f.tick()

	if len(f.notices) != 1 {
		t.Fatalf("notices = %q, want one", f.notices)
	}
	if !strings.Contains(f.notices[0], "you return as a successor that reads your handoff") {
		t.Fatalf("notice = %q, want it to say the seat returns as a successor reading its handoff", f.notices[0])
	}
}
