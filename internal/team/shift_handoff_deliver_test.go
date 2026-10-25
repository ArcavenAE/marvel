package team

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arcavenae/marvel/contracts/go/envelope"
	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// Red tests for delivering team.shift-handoff-missing to the team's supervisor
// (marvel#459, docs/design/handoff-missing-delivery.md, rulings of 2026-10-02).
// They cover the parts that do not depend on the open questions: the envelope
// (H2), persistence and idempotency across a restart (H4), the dedupe header,
// no recipient (H1) and a bus that is down (H5).

const testSupervisorRole = "supervisor"

// sentEnvelope is one PublishRoleInbox call.
type sentEnvelope struct {
	ws, team, role string
	env            envelope.Envelope
	msgID          string
}

// fakePublisher records calls. fail, when set, is returned from every call;
// block makes a call wait for its context, the way a dead bus does.
type fakePublisher struct {
	mu    sync.Mutex
	sent  []sentEnvelope
	calls int
	fail  error
	block bool
}

func (p *fakePublisher) PublishRoleInbox(ctx context.Context, ws, team, role string, env envelope.Envelope, msgID string) error {
	p.mu.Lock()
	p.calls++
	fail, block := p.fail, p.block
	p.mu.Unlock()
	if block {
		<-ctx.Done()
		return ctx.Err()
	}
	if fail != nil {
		return fail
	}
	p.mu.Lock()
	p.sent = append(p.sent, sentEnvelope{ws, team, role, env, msgID})
	p.mu.Unlock()
	return nil
}

func (p *fakePublisher) delivered() []sentEnvelope {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]sentEnvelope(nil), p.sent...)
}

func (p *fakePublisher) attempts() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *fakePublisher) set(fail error, block bool) {
	p.mu.Lock()
	p.fail, p.block = fail, block
	p.mu.Unlock()
}

// supervisorRole is a single-replica role named supervisor. marvel keys on a
// live session of this role and does not look at the role's env: director's
// launcher sets DIRECTOR_ROLE for the seats it wraps, which marvel cannot see
// (architect answers on marvel#459, 2026-10-25).
func supervisorRole() api.Role {
	return api.Role{
		Name:     testSupervisorRole,
		Replicas: 1,
		Runtime:  api.Runtime{Name: "claude", Command: "claude"},
	}
}

// deliverFixture is a list fixture whose team also has a supervisor role and a
// recording publisher.
type deliverFixture struct {
	*listFixture
	pub *fakePublisher
	dir string
}

func newDeliverFixture(t *testing.T, ws string, withSupervisor, supervisorRunning bool, conds ...api.ShiftCondition) *deliverFixture {
	t.Helper()
	dir := t.TempDir()
	f := &deliverFixture{listFixture: newListFixture(t, ws, maxAgeRole(dir, conds...)), pub: &fakePublisher{}, dir: dir}
	f.ctrl.Publisher = f.pub
	if withSupervisor {
		if err := f.store.UpdateTeam(f.teamKey, func(live *api.Team) error {
			live.Roles = append(live.Roles, supervisorRole())
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if supervisorRunning {
			f.seedSupervisor()
		}
	}
	return f
}

func (f *deliverFixture) seedSupervisor() {
	f.t.Helper()
	s := api.Session{
		Name:       "squad-" + testSupervisorRole + "-g1-0",
		Workspace:  strings.TrimSuffix(f.teamKey, "/squad"),
		Team:       "squad",
		Role:       testSupervisorRole,
		Generation: 1,
		State:      api.SessionRunning,
		CreatedAt:  f.clock.Now(),
	}
	seedSession(f.t, f.store, s)
}

// escalate drives the worker's request to its escalation, as
// TestMaxAgeEscalatesWithoutMarker does, and returns the team.
func (f *deliverFixture) escalate() api.Team {
	f.t.Helper()
	f.evaluate()
	f.clock.Advance(api.DefaultShiftHandoffWindow + time.Second)
	f.evaluate() // starts the read that grounds the escalation
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Second)
	got := f.evaluate()
	f.ctrl.handoffProbes.wait()
	if !got.ShiftRequests[testShiftRole].Escalated {
		f.t.Fatalf("setup: request not escalated: %+v", got.ShiftRequests)
	}
	return got
}

// nextWindow advances one handoff window and runs the two ticks a repeat needs.
func (f *deliverFixture) nextWindow() api.Team {
	f.t.Helper()
	f.clock.Advance(api.DefaultShiftHandoffWindow + time.Second)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Second)
	got := f.evaluate()
	f.ctrl.handoffProbes.wait()
	return got
}

func envText(t *testing.T, e envelope.Envelope) string {
	t.Helper()
	s, ok := e.Content.Data.(string)
	if !ok {
		t.Fatalf("content.data = %#v, want a string", e.Content.Data)
	}
	return s
}

// Test 1 (envelope, H1 and H2): the escalation is published once to the
// supervisor role, as a REQUEST signal from marvel that validates against the
// canonical schema, and says which cause fired.
func TestEscalationPublishesOneEnvelopeToTheSupervisorRole(t *testing.T) {
	for _, tc := range []struct {
		name  string
		conds []api.ShiftCondition
		tok   int
		cause string
		text  string
	}{
		{"max age", nil, 0, "max-age", "max age"},
		{"context pressure", []api.ShiftCondition{{On: api.ShiftTriggerContextPressure, HeadroomTokens: testShiftHeadroom}}, 900_000, "context-pressure", "context"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDeliverFixture(t, "test-dlv-env-"+tc.cause, true, true, tc.conds...)
			s := f.seed(testMaxAge+time.Hour, 10*time.Minute, tc.tok, 1_000_000)
			f.escalate()

			got := f.pub.delivered()
			if len(got) != 1 {
				t.Fatalf("published %d envelopes, want 1", len(got))
			}
			m := got[0]
			ws := strings.TrimSuffix(f.teamKey, "/squad")
			if m.ws != ws || m.team != "squad" || m.role != testSupervisorRole {
				t.Fatalf("published to %s/%s role %s, want %s/squad role %s", m.ws, m.team, m.role, ws, testSupervisorRole)
			}
			raw, err := json.Marshal(m.env)
			if err != nil {
				t.Fatal(err)
			}
			if err := envelope.Validate(raw); err != nil {
				t.Fatalf("envelope does not validate against the canonical schema: %v\n%s", err, raw)
			}
			e := m.env
			if e.Performative != envelope.PerformativeREQUEST || e.Content.Type != envelope.TypeSignal {
				t.Fatalf("performative %q content.type %q, want REQUEST and signal", e.Performative, e.Content.Type)
			}
			if e.Sender.AgentID != "marvel" || e.Sender.Workspace != ws {
				t.Fatalf("sender = %+v, want agent marvel in workspace %s", e.Sender, ws)
			}
			if e.Recipient.Address != "role://squad/"+testSupervisorRole {
				t.Fatalf("recipient = %q", e.Recipient.Address)
			}
			if e.Authority == nil || e.Authority.Strength != envelope.StrengthNone {
				t.Fatalf("authority = %+v, want strength none", e.Authority)
			}
			text := envText(t, e)
			for _, want := range []string{"handoff-missing", s.Key(), "marvel shift " + f.teamKey + " --role " + testShiftRole, tc.text} {
				if !strings.Contains(text, want) {
					t.Errorf("content.data %q does not contain %q", text, want)
				}
			}
			if tc.cause == "context-pressure" && strings.Contains(text, "max age") {
				t.Errorf("content.data %q names max age for a context-pressure escalation", text)
			}
			wantCorr := fmt.Sprintf("handoff-missing/%s/squad/%s/%d", ws, s.Name, listEpoch.Unix())
			if e.CorrelationID == nil || *e.CorrelationID != wantCorr {
				t.Fatalf("correlation_id = %v, want %q", e.CorrelationID, wantCorr)
			}
			if e.ReplyBy == nil || e.ExpiresAt == nil || !e.ExpiresAt.After(*e.ReplyBy) {
				t.Fatalf("reply_by %v expires_at %v, want both set with expiry after the next repeat", e.ReplyBy, e.ExpiresAt)
			}
			if e.MessageID == "" || m.msgID != wantCorr+"/0" {
				t.Fatalf("message_id %q msgID %q, want a message id and the dedupe id %q", e.MessageID, m.msgID, wantCorr+"/0")
			}
			if n := f.count(events.KindShiftHandoffMissing); n != 1 {
				t.Fatalf("handoff-missing events = %d, want 1: the event stays the floor", n)
			}
			for _, ev := range f.ring.Snapshot(events.Filter{Kind: events.KindShiftHandoffMissing}, 0) {
				if !strings.Contains(ev.Message, "published: sent") {
					t.Errorf("event %q does not say whether it was published", ev.Message)
				}
			}
			if len(f.notices) != 1 {
				t.Fatalf("notices = %d, want 1: delivery never types into any pane", len(f.notices))
			}
		})
	}
}

// Test 2 (H2, H4): a repeat after one window carries the same correlation_id,
// a new message_id and the next dedupe id; the count is persisted with the
// request; a shift ends the request and nothing follows.
func TestRepeatKeepsTheCorrelationIdAndAdvancesTheDedupeId(t *testing.T) {
	f := newDeliverFixture(t, "test-deliver-repeat", true, true)
	f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)
	f.escalate()
	got := f.nextWindow()

	sent := f.pub.delivered()
	if len(sent) != 2 {
		t.Fatalf("published %d envelopes after one window, want 2", len(sent))
	}
	a, b := sent[0], sent[1]
	if *a.env.CorrelationID != *b.env.CorrelationID {
		t.Fatalf("correlation ids differ: %q vs %q", *a.env.CorrelationID, *b.env.CorrelationID)
	}
	if a.env.MessageID == b.env.MessageID {
		t.Fatalf("message_id repeated: %q", a.env.MessageID)
	}
	if !strings.HasSuffix(a.msgID, "/0") || !strings.HasSuffix(b.msgID, "/1") {
		t.Fatalf("dedupe ids %q, %q, want suffixes /0 and /1", a.msgID, b.msgID)
	}
	req := got.ShiftRequests[testShiftRole]
	if age := f.clock.Now().Sub(req.LastDeliveredAt); req.DeliveredCount != 2 || age < 0 || age > 5*time.Second {
		t.Fatalf("request = count %d last %v, want 2 and a send just now (%v)", req.DeliveredCount, req.LastDeliveredAt, f.clock.Now())
	}

	// A shift ends the request; no further envelope follows.
	if err := f.store.UpdateTeam(f.teamKey, func(live *api.Team) error {
		delete(live.ShiftRequests, testShiftRole)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.nextWindow()
	if n := len(f.pub.delivered()); n != 2 {
		t.Fatalf("published %d envelopes after the request ended, want still 2", n)
	}
}

// Test 3 (no recipient, H1): a team with no supervisor role publishes nothing
// and types into no pane; the event continues and says why.
func TestNoSupervisorRolePublishesNothing(t *testing.T) {
	f := newDeliverFixture(t, "test-deliver-norecipient", false, false)
	f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)
	f.escalate()
	f.nextWindow()

	if n := f.pub.attempts(); n != 0 {
		t.Fatalf("publisher called %d times, want 0", n)
	}
	if len(f.notices) != 1 {
		t.Fatalf("notices = %d, want only the original handoff request", len(f.notices))
	}
	evs := f.ring.Snapshot(events.Filter{Kind: events.KindShiftHandoffMissing}, 0)
	if len(evs) < 2 {
		t.Fatalf("handoff-missing events = %d, want the floor to keep repeating", len(evs))
	}
	for _, ev := range evs {
		if !strings.Contains(ev.Message, "published: none, no recipient") {
			t.Errorf("event %q does not say there is no recipient", ev.Message)
		}
	}
}

// Test 4 (H1): a supervisor role with no live seat gets exactly one envelope
// across six windows. When a holder appears, an immediate send follows and the
// normal cadence resumes.
func TestSupervisorWithNoLiveSeatIsPublishedToOnce(t *testing.T) {
	f := newDeliverFixture(t, "test-deliver-queued", true, false)
	f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)
	f.escalate()
	for i := 0; i < 5; i++ {
		f.nextWindow()
	}
	if n := len(f.pub.delivered()); n != 1 {
		t.Fatalf("published %d envelopes across six windows with no holder, want 1", n)
	}
	evs := f.ring.Snapshot(events.Filter{Kind: events.KindShiftHandoffMissing}, 0)
	if !strings.Contains(evs[0].Message, "published: queued once, no live holder") {
		t.Errorf("first event %q does not say the message was queued once", evs[0].Message)
	}

	f.seedSupervisor()
	f.clock.Advance(time.Second)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	if n := len(f.pub.delivered()); n != 2 {
		t.Fatalf("published %d envelopes after a holder appeared, want an immediate 2nd", n)
	}
	f.nextWindow()
	if n := len(f.pub.delivered()); n != 3 {
		t.Fatalf("published %d envelopes one window later, want the cadence to resume at 3", n)
	}
}

// Test 5a (H4): the count and time are persisted with the request, and a
// restarted controller on the same store sends again only at the next window
// boundary.
func TestRestartDoesNotProduceAnExtraEnvelope(t *testing.T) {
	f := newDeliverFixture(t, "test-deliver-restart", true, true)
	f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)
	got := f.escalate()
	req := got.ShiftRequests[testShiftRole]
	if req.DeliveredCount != 1 || req.LastDeliveredAt.IsZero() {
		t.Fatalf("request = count %d last %v, want 1 and a time", req.DeliveredCount, req.LastDeliveredAt)
	}

	fresh := NewController(f.store, f.ctrl.sessMgr)
	fresh.now = f.clock.Now
	fresh.Events = f.ring
	fresh.Notify = f.ctrl.Notify
	fresh.Publisher = f.pub
	f.ctrl = fresh

	f.clock.Advance(api.DefaultShiftHandoffWindow / 2)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Second)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	if n := len(f.pub.delivered()); n != 1 {
		t.Fatalf("published %d envelopes after a restart inside the window, want still 1", n)
	}
	f.nextWindow()
	if n := len(f.pub.delivered()); n != 2 {
		t.Fatalf("published %d envelopes after the boundary, want 2", n)
	}
}

// Test 5b (H4 dedupe): a send that failed is not counted, and its retry carries
// the same dedupe id, so a publish that landed before the daemon could record it
// is dropped by the stream rather than doubled.
func TestFailedSendIsNotCountedAndRetriesWithTheSameDedupeId(t *testing.T) {
	f := newDeliverFixture(t, "test-deliver-dedupe", true, true)
	f.pub.set(errors.New("nats: connection closed"), false)
	f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)
	got := f.escalate()
	if c := got.ShiftRequests[testShiftRole].DeliveredCount; c != 0 {
		t.Fatalf("DeliveredCount = %d after a failed send, want 0", c)
	}
	if f.pub.attempts() != 1 {
		t.Fatalf("attempts = %d, want 1", f.pub.attempts())
	}

	f.pub.set(nil, false)
	f.nextWindow()
	sent := f.pub.delivered()
	if len(sent) != 1 || !strings.HasSuffix(sent[0].msgID, "/0") {
		t.Fatalf("delivered = %+v, want one envelope with the first dedupe id (/0)", sent)
	}
}

// Test 6 (H5): a dead bus never holds the reconcile tick past the publish
// timeout, announces itself once per transition with
// team.shift-handoff-undelivered, keeps the event repeating, and a send follows
// the bus becoming ready without waiting a whole window.
func TestBusDownDoesNotBlockTheTickAndRecovers(t *testing.T) {
	f := newDeliverFixture(t, "test-deliver-busdown", true, true)
	ready := false
	f.ctrl.BusGate = func() (bool, string) {
		if ready {
			return true, ""
		}
		return false, "bus down"
	}
	f.pub.set(nil, true)
	f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)

	f.evaluate()
	f.clock.Advance(api.DefaultShiftHandoffWindow + time.Second)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Second)
	start := time.Now()
	f.evaluate()
	if took := time.Since(start); took > 4*time.Second {
		t.Fatalf("the escalation tick took %v with a dead bus, want it bounded by the 2s publish timeout", took)
	}
	f.ctrl.handoffProbes.wait()
	if f.pub.attempts() == 0 {
		t.Fatal("publisher never called: the test would pass without delivery")
	}
	if n := f.count(events.KindShiftHandoffUndelivered); n != 1 {
		t.Fatalf("undelivered events = %d, want 1", n)
	}
	if n := f.count(events.KindShiftHandoffMissing); n != 1 {
		t.Fatalf("handoff-missing events = %d, want the floor to stand", n)
	}

	// Still down a window later: the event repeats, the undelivered notice does not.
	f.nextWindow()
	if n := f.count(events.KindShiftHandoffUndelivered); n != 1 {
		t.Fatalf("undelivered events = %d after another failure, want still 1 (once per transition)", n)
	}

	// The bus returns: a send follows within a health tick, not a full window.
	f.pub.set(nil, false)
	ready = true
	f.clock.Advance(35 * time.Second)
	got := f.evaluate()
	f.ctrl.handoffProbes.wait()
	if n := len(f.pub.delivered()); n != 1 {
		t.Fatalf("delivered %d envelopes 35s after the bus returned, want 1", n)
	}
	if c := got.ShiftRequests[testShiftRole].DeliveredCount; c != 1 {
		t.Fatalf("DeliveredCount = %d, want 1", c)
	}
}
