package team

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/contracts/go/envelope"
	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// The escalation legs the design left for later (docs/design/handoff-missing-delivery.md
// H1, tests 11 and 14, and marvel#1309 and #1311): a supervisor seat that
// misses its window reaches the other supervisors and the director, and a team
// with no supervisor role reaches the director. The director gets a notice
// once and at most one repeat per request, and only while the leaf is up.

const stuckPast = 31 * time.Minute // past the default max_defer of 30m

// legFixture is a team whose max-age role is itself named supervisor, with two
// replicas: the old one is escalated and the young one is its other replica.
type legFixture struct {
	*deliverFixture
	leaf *bool
}

func newLegFixture(t *testing.T, ws string, replicas int) *legFixture {
	t.Helper()
	role := maxAgeRole(t.TempDir())
	role.Name = testSupervisorRole
	role.Replicas = replicas
	f := &legFixture{deliverFixture: &deliverFixture{listFixture: newListFixture(t, ws, role), pub: &fakePublisher{}}}
	f.ctrl.Publisher = f.pub
	up := false
	f.leaf = &up
	f.ctrl.LeafUp = func() bool { return *f.leaf }
	f.seedReplica(0, testMaxAge+time.Hour)
	for i := 1; i < replicas; i++ {
		f.seedReplica(i, time.Minute)
	}
	return f
}

func (f *legFixture) ws() string { return strings.TrimSuffix(f.teamKey, "/squad") }

func (f *legFixture) seedReplica(i int, age time.Duration) {
	f.t.Helper()
	s := api.Session{
		Name: "squad-" + testSupervisorRole + "-g1-" + string(rune('0'+i)), Workspace: f.ws(), Team: "squad",
		Role: testSupervisorRole, Generation: 1, State: api.SessionRunning, CreatedAt: f.clock.Now().Add(-age),
	}
	seedSession(f.t, f.store, s)
	f.store.UpdateSessionContext(s.Key(), api.SessionContext{ContextAt: f.clock.Now().Add(-10 * time.Minute)})
}

// otherTeam adds a team to the same workspace whose supervisor role is running
// when running is set.
func (f *legFixture) otherTeam(name string, running bool) {
	f.t.Helper()
	otherTeamIn(f.t, f.store, f.ws(), name, running, f.clock.Now())
}

func otherTeamIn(t *testing.T, store *api.Store, ws, name string, running bool, now time.Time) {
	t.Helper()
	if err := store.CreateTeam(&api.Team{Name: name, Workspace: ws, Generation: 1, Roles: []api.Role{supervisorRole()}}); err != nil {
		t.Fatal(err)
	}
	// A team whose supervisor is not running still has a session record.
	state := api.SessionRunning
	if !running {
		state = api.SessionCrashed
	}
	seedSession(t, store, api.Session{
		Name: name + "-supervisor-g1-0", Workspace: ws, Team: name, Role: testSupervisorRole,
		Generation: 1, State: state, CreatedAt: now,
	})
}

// escalateRole drives the request for role to its escalation, as escalate does
// for the worker role.
func (f *legFixture) escalateRole() api.ShiftRequest {
	f.t.Helper()
	f.evaluate()
	f.clock.Advance(api.DefaultShiftHandoffWindow + time.Second)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Second)
	got := f.evaluate()
	f.ctrl.handoffProbes.wait()
	req := got.ShiftRequests[testSupervisorRole]
	if !req.Escalated {
		f.t.Fatalf("setup: request not escalated: %+v", got.ShiftRequests)
	}
	return req
}

func (f *legFixture) tick(d time.Duration) {
	f.t.Helper()
	f.clock.Advance(d)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
}

func (f *legFixture) missingEvents() []events.Event {
	return f.ring.Snapshot(events.Filter{Kind: events.KindShiftHandoffMissing}, 0)
}

func (f *legFixture) lastPublished() string {
	evs := f.missingEvents()
	if len(evs) == 0 {
		f.t.Fatal("no handoff-missing event")
	}
	m := evs[len(evs)-1].Message
	_, after, _ := strings.Cut(m, "; published: ")
	return after
}

// Test 11, replicas: an escalated supervisor reaches its other live replica by
// its own inbox, never by the role address, and nothing reaches the escalated
// seat's own durable.
func TestEscalatedSupervisorReachesItsOtherReplicaByAgentAddress(t *testing.T) {
	f := newLegFixture(t, "test-legs-replica", 2)

	f.escalateRole()

	seats := f.pub.seats()
	if len(seats) != 1 {
		t.Fatalf("seat sends = %d, want 1: %+v", len(seats), seats)
	}
	s := seats[0]
	if s.team != "squad" || s.role != "squad-supervisor-g1-1" {
		t.Errorf("sent to team %q agent %q, want squad / squad-supervisor-g1-1", s.team, s.role)
	}
	if s.env.Recipient.Address != "agent://squad/squad-supervisor-g1-1" {
		t.Errorf("recipient address = %q", s.env.Recipient.Address)
	}
	if got := f.pub.delivered(); len(got) != 0 {
		t.Errorf("role sends = %+v: the role address would reach the escalated seat's own durable", got)
	}
	data := jsonOf(t, s.env)
	if err := envelope.Validate(data); err != nil {
		t.Errorf("the envelope does not validate: %v", err)
	}
	if len(f.notices) != 1 {
		t.Errorf("notices = %d, want only the original request: nothing is typed into a pane", len(f.notices))
	}
}

// Test 11, other teams: each other team in the same workspace with a running
// supervisor is reached by its role address; a team without a running
// supervisor, and a team in another workspace, is not.
func TestEscalatedSupervisorReachesOtherTeamsSupervisorsInItsWorkspace(t *testing.T) {
	f := newLegFixture(t, "test-legs-teams", 1)
	f.otherTeam("ops", true)
	f.otherTeam("idle", false)
	otherTeamIn(t, f.store, "elsewhere", "far", true, f.clock.Now())

	f.escalateRole()

	got := f.pub.delivered()
	if len(got) != 1 || got[0].team != "ops" || got[0].role != testSupervisorRole || got[0].ws != f.ws() {
		t.Fatalf("role sends = %+v, want one to ops/supervisor in %s", got, f.ws())
	}
	if got[0].env.Recipient.Address != "role://ops/supervisor" {
		t.Errorf("recipient address = %q", got[0].env.Recipient.Address)
	}
	if len(f.pub.seats()) != 0 || len(f.pub.director()) != 0 {
		t.Errorf("unexpected seat or director sends: %+v %+v", f.pub.seats(), f.pub.director())
	}
}

// Test 11, nothing to send: with no other supervisor and no leaf, nothing is
// published and the event says why.
func TestEscalatedSupervisorWithNobodyToTellSaysSo(t *testing.T) {
	f := newLegFixture(t, "test-legs-nobody", 1)

	f.escalateRole()
	f.tick(api.DefaultShiftHandoffWindow + time.Second)

	if f.pub.attempts() != 0 {
		t.Fatalf("publisher called %d times, want 0", f.pub.attempts())
	}
	if got := f.lastPublished(); !strings.Contains(got, "none, no other supervisor and no global tier") {
		t.Errorf("published clause %q does not say why nothing was sent", got)
	}
}

// Test 14 (the controller's half): the director leg is off while the leaf is
// not up, and the event says so; once the leaf is up the director gets one
// REQUEST addressed to global://director that validates against the schema.
func TestDirectorLegWaitsForTheLeaf(t *testing.T) {
	f := newLegFixture(t, "test-legs-leaf", 1)

	f.escalateRole()
	if len(f.pub.director()) != 0 {
		t.Fatal("the director was published to while the leaf was not up")
	}
	if got := f.lastPublished(); !strings.Contains(got, "director off") {
		t.Errorf("published clause %q does not name the director leg as off", got)
	}

	*f.leaf = true
	f.tick(time.Second)

	d := f.pub.director()
	if len(d) != 1 {
		t.Fatalf("director sends = %d, want 1", len(d))
	}
	if d[0].env.Recipient.Address != "global://director" {
		t.Errorf("recipient address = %q", d[0].env.Recipient.Address)
	}
	if d[0].env.Performative != envelope.PerformativeREQUEST || d[0].env.Authority == nil || d[0].env.Authority.Strength != envelope.StrengthNone {
		t.Errorf("the director notice must be a REQUEST with authority none: %+v", d[0].env)
	}
	data := jsonOf(t, d[0].env)
	if err := envelope.Validate(data); err != nil {
		t.Errorf("the envelope does not validate: %v", err)
	}
	// The event repeats once per window, and its clause then says what the
	// director got.
	f.tick(api.DefaultShiftHandoffWindow + time.Second)
	if got := f.lastPublished(); !strings.Contains(got, "director sent 1 of 2") {
		t.Errorf("published clause %q does not record the director send", got)
	}
}

// With no leaf the cluster never gets a leaf-gated send, however many windows
// pass (condition 1: with no director, today's behavior holds).
func TestNoLeafMeansNoDirectorSendAcrossManyWindows(t *testing.T) {
	f := newLegFixture(t, "test-legs-noleaf", 1)
	f.ctrl.LeafUp = nil

	f.escalateRole()
	for i := 0; i < 6; i++ {
		f.tick(api.DefaultShiftHandoffWindow + time.Second)
	}

	if f.pub.attempts() != 0 {
		t.Errorf("publisher called %d times with no leaf gate, want 0", f.pub.attempts())
	}
}

// Condition 3: the director gets one send, and one repeat when the request
// crosses its next threshold (team.shift-request-stuck), and never more, however
// long the request stays stuck.
func TestDirectorGetsOneSendAndOneRepeatAtTheStuckEvent(t *testing.T) {
	f := newLegFixture(t, "test-legs-cap", 1)
	*f.leaf = true

	f.escalateRole()
	for i := 0; i < 3; i++ {
		f.tick(api.DefaultShiftHandoffWindow + time.Second)
	}
	if n := len(f.pub.director()); n != 1 {
		t.Fatalf("director sends over three windows before the stuck event = %d, want 1", n)
	}
	if n := f.count(events.KindShiftRequestStuck); n != 0 {
		t.Fatalf("setup: stuck events = %d before the threshold", n)
	}

	f.tick(stuckPast)
	f.tick(time.Second)
	if n := f.count(events.KindShiftRequestStuck); n != 1 {
		t.Fatalf("setup: stuck events = %d, want 1", n)
	}
	if n := len(f.pub.director()); n != 2 {
		t.Fatalf("director sends after the stuck event = %d, want 2", n)
	}

	evs := f.ring.Snapshot(events.Filter{Kind: events.KindShiftRequestStuck}, 0)
	if len(evs) != 1 {
		t.Fatalf("stuck events = %d, want 1 to read the director count from", len(evs))
	}
	if !strings.Contains(evs[0].Message, "1 of 2 notices published to the director") {
		t.Errorf("stuck event %q does not say what was published to the director", evs[0].Message)
	}

	for i := 0; i < 10; i++ {
		f.tick(api.DefaultShiftHandoffWindow + time.Second)
	}
	f.tick(100 * time.Hour)
	if n := len(f.pub.director()); n != 2 {
		t.Errorf("director sends after ten more windows = %d, want still 2", n)
	}
	// The two sends carry one correlation id and two dedupe ids.
	d := f.pub.director()
	if d[0].msgID == d[1].msgID {
		t.Errorf("the two director sends share the dedupe id %q", d[0].msgID)
	}
	if *d[0].env.CorrelationID != *d[1].env.CorrelationID {
		t.Errorf("correlation ids differ: %q and %q", *d[0].env.CorrelationID, *d[1].env.CorrelationID)
	}
	if got := f.lastPublished(); !strings.Contains(got, "director sent 2 of 2") {
		t.Errorf("published clause %q does not record the cap reached", got)
	}
}

// A team with no supervisor role reaches the director under the same cap, and
// the event names why nobody else was told. It is a notice: nothing is typed
// into a pane and the seat is not shifted.
func TestNoSupervisorTeamReachesTheDirectorUnderTheSameCap(t *testing.T) {
	f := newDeliverFixture(t, "test-legs-nosup", false, false)
	up := true
	f.ctrl.LeafUp = func() bool { return up }
	f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)

	f.escalate()
	for i := 0; i < 3; i++ {
		f.nextWindow()
	}
	if n := len(f.pub.director()); n != 1 {
		t.Fatalf("director sends before the stuck event = %d, want 1", n)
	}
	f.clock.Advance(stuckPast)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Second)
	f.evaluate()
	for i := 0; i < 8; i++ {
		f.nextWindow()
	}
	f.clock.Advance(100 * time.Hour)
	got := f.evaluate()

	d := f.pub.director()
	if len(d) != 2 {
		t.Fatalf("director sends = %d, want 2 at most", len(d))
	}
	if d[0].env.Recipient.Address != "global://director" {
		t.Errorf("recipient address = %q", d[0].env.Recipient.Address)
	}
	if len(f.pub.delivered())+len(f.pub.seats()) != 0 {
		t.Errorf("a role or seat was published to: %+v %+v", f.pub.delivered(), f.pub.seats())
	}
	if len(f.notices) != 1 {
		t.Errorf("notices = %d, want only the original handoff request", len(f.notices))
	}
	if got.Shift.Phase != api.ShiftNone {
		t.Errorf("phase = %q: a notice never changes state", got.Shift.Phase)
	}
	evs := f.ring.Snapshot(events.Filter{Kind: events.KindShiftHandoffMissing}, 0)
	last := evs[len(evs)-1].Message
	if !strings.Contains(last, "published: none, no recipient") || !strings.Contains(last, "director sent 2 of 2") {
		t.Errorf("event %q does not say there is no supervisor role and that the director got 2 of 2", last)
	}
}

// Condition 1: with the leaf down the no-supervisor team behaves as today, and
// the event names the reason nothing was sent.
func TestNoSupervisorTeamWithTheLeafDownNamesWhyNothingWasSent(t *testing.T) {
	f := newDeliverFixture(t, "test-legs-nosup-down", false, false)
	f.ctrl.LeafUp = func() bool { return false }
	f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)

	f.escalate()
	f.nextWindow()

	if f.pub.attempts() != 0 {
		t.Fatalf("publisher called %d times, want 0", f.pub.attempts())
	}
	evs := f.ring.Snapshot(events.Filter{Kind: events.KindShiftHandoffMissing}, 0)
	for _, ev := range evs {
		if !strings.Contains(ev.Message, "published: none, no recipient") || !strings.Contains(ev.Message, "director off") {
			t.Errorf("event %q does not name the missing supervisor role and the director leg being off", ev.Message)
		}
	}
}

// The cap is on the request, so a restart does not give the director a third
// send: a new controller on the same store sends nothing more.
func TestDirectorCapSurvivesARestart(t *testing.T) {
	f := newLegFixture(t, "test-legs-restart", 1)
	*f.leaf = true
	f.escalateRole()
	f.tick(stuckPast)
	f.tick(time.Second)
	if n := len(f.pub.director()); n != 2 {
		t.Fatalf("setup: director sends = %d, want 2", n)
	}

	fresh := NewController(f.store, f.ctrl.sessMgr)
	fresh.now = f.clock.Now
	fresh.Events = f.ring
	fresh.Notify = f.ctrl.Notify
	fresh.Publisher = f.pub
	fresh.LeafUp = f.ctrl.LeafUp
	team, _ := f.store.GetTeam(f.teamKey)
	for i := 0; i < 4; i++ {
		f.clock.Advance(stuckPast)
		fresh.evaluateShiftTriggers(&team)
		fresh.handoffProbes.wait()
		team, _ = f.store.GetTeam(f.teamKey)
	}

	if n := len(f.pub.director()); n != 2 {
		t.Errorf("director sends after a restart = %d, want still 2", n)
	}
}

// A send that fails is not counted against the cap, so the director's two
// sends are two that arrived; the transition is reported once.
func TestAFailedDirectorSendIsNotCounted(t *testing.T) {
	f := newLegFixture(t, "test-legs-fail", 1)
	*f.leaf = true
	f.pub.set(errors.New("bus down"), false)

	req := f.escalateRole()
	f.tick(api.DefaultShiftHandoffWindow + time.Second)
	if req.DirectorSent != 0 || len(f.pub.director()) != 0 {
		t.Fatalf("a failed send was counted: %+v", req)
	}
	if n := f.count(events.KindShiftHandoffUndelivered); n != 1 {
		t.Errorf("undelivered events = %d, want 1 per transition", n)
	}

	f.pub.set(nil, false)
	f.tick(api.DefaultShiftHandoffWindow + time.Second)
	if n := len(f.pub.director()); n != 1 {
		t.Errorf("director sends after the bus came back = %d, want 1", n)
	}
}

// The legs a supervisor's escalation uses repeat on the normal cadence, but
// the director's cap does not follow them: with a replica receiving every
// window, the director still has one send.
func TestReplicaLegRepeatsEveryWindowWhileTheDirectorKeepsItsCap(t *testing.T) {
	f := newLegFixture(t, "test-legs-cadence", 2)
	*f.leaf = true

	f.escalateRole()
	for i := 0; i < 3; i++ {
		f.tick(api.DefaultShiftHandoffWindow + time.Second)
	}

	if n := len(f.pub.seats()); n != 4 {
		t.Errorf("replica sends over four windows = %d, want 4", n)
	}
	if n := len(f.pub.director()); n != 1 {
		t.Errorf("director sends = %d, want 1", n)
	}
	if got := f.lastPublished(); !strings.Contains(got, "replicas sent") || !strings.Contains(got, "director sent 1 of 2") {
		t.Errorf("published clause %q does not list the legs", got)
	}
}

// The replica leg backs off like the supervisor leg: four sends one window
// apart, then the next waits two windows, not one.
func TestReplicaLegBacksOffAfterTheFlatRepeats(t *testing.T) {
	f := newLegFixture(t, "test-legs-backoff", 2)
	f.escalateRole()
	for i := 0; i < 3; i++ {
		f.tick(api.DefaultShiftHandoffWindow + time.Second)
	}
	if n := len(f.pub.seats()); n != 4 {
		t.Fatalf("replica sends over four windows = %d, want 4", n)
	}
	f.tick(api.DefaultShiftHandoffWindow + time.Second)
	if n := len(f.pub.seats()); n != 4 {
		t.Errorf("a fifth send went out one window after the fourth: %d sends, want it to wait two windows", n)
	}
	f.tick(api.DefaultShiftHandoffWindow + time.Second)
	if n := len(f.pub.seats()); n != 5 {
		t.Errorf("replica sends after two windows = %d, want 5", n)
	}
}

// Name problems still read as before for a supervisor: no managed bus and a
// team name the schema cannot address.
func TestSupervisorEscalationStillNamesNoBusAndUnaddressableTeams(t *testing.T) {
	f := newLegFixture(t, "test-legs-nobus", 1)
	f.ctrl.Publisher = nil
	f.escalateRole()
	if got := f.lastPublished(); !strings.Contains(got, "none, no managed bus") {
		t.Errorf("published clause %q", got)
	}

	g := newLegFixture(t, "Test_Legs_Upper", 1)
	g.escalateRole()
	if got := g.lastPublished(); !strings.Contains(got, "none, team name not addressable") {
		t.Errorf("published clause %q", got)
	}
	if g.pub.attempts() != 0 {
		t.Errorf("publisher called %d times for an unaddressable team", g.pub.attempts())
	}
}

func jsonOf(t *testing.T, env envelope.Envelope) []byte {
	t.Helper()
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// A team that had no supervisor role and then gains one must stop reporting
// "no recipient" at a tick where nothing is due: the event reads the persisted
// outcome once the role exists.
func TestStaleNoRecipientTextIsReplacedWhenTheTeamGainsASupervisorRole(t *testing.T) {
	f := newDeliverFixture(t, "test-legs-gains-sup", false, false)
	f.ctrl.LeafUp = func() bool { return false }
	sess := f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)
	f.escalate()
	if got := f.ctrl.publishRecords[sess.Key()].outcome; !strings.HasPrefix(got, "none, no recipient") {
		t.Fatalf("setup: outcome = %q, want the no-recipient text", got)
	}

	// The role arrives with one queued delivery already on the request and no
	// running holder, so the next tick has nothing due.
	if err := f.store.UpdateTeam(f.teamKey, func(live *api.Team) error {
		live.Roles = append(live.Roles, supervisorRole())
		req := live.ShiftRequests[testShiftRole]
		req.DeliveredCount, req.LastDeliveryQueued, req.LastDeliveredAt = 1, true, f.clock.Now()
		live.ShiftRequests[testShiftRole] = req
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(time.Second)
	f.evaluate()
	f.ctrl.handoffProbes.wait()

	if got := f.ctrl.publishRecords[sess.Key()].outcome; got != "queued once, no live holder" {
		t.Errorf("outcome = %q, want %q", got, "queued once, no live holder")
	}
}
