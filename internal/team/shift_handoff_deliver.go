package team

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"regexp"
	"time"

	"github.com/arcavenae/marvel/contracts/go/envelope"
	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// Delivery of team.shift-handoff-missing to the team's supervisor
// (marvel#459, docs/design/handoff-missing-delivery.md). The escalation used to
// reach only the in-memory event ring. Now marvel also publishes one envelope
// to role://<team>/supervisor at escalation and once per handoff window, and
// the event says whether it was published. marvel never reads a reply: the
// supervisor's decision is an action (marvel shift), which marvel observes.

// HandoffPublisher is the controller's only bus capability: it publishes one
// envelope to a role's inbox. The implementation builds the subject itself
// from validated tokens and can publish nowhere else, because the principal
// behind it can publish to ">" (docs/design/handoff-missing-delivery.md,
// section 4). Nil means no managed bus, which is event-only escalation.
type HandoffPublisher interface {
	PublishRoleInbox(ctx context.Context, ws, team, role string, env envelope.Envelope, msgID string) error
}

const (
	// supervisorRoleName is the recipient role (design H1, ruled 2026-10-02).
	supervisorRoleName = "supervisor"
	// publishTimeout bounds one publish, so a dead bus cannot hold the
	// reconcile tick (design H5).
	publishTimeout = 2 * time.Second
	// escalationSender is the envelope's sender agent id. It is a label, not
	// authentication; authority "none" keeps it to a request for a decision.
	escalationSender = "marvel"
)

// addressable is the canonical schema's character class for one address
// segment. A name outside it cannot be put in a role address.
var addressable = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// publishRecord is what the controller remembers, in memory, about publishing
// one escalated session's request. The durable half (the count and time) lives
// on the request itself.
type publishRecord struct {
	outcome     string // what the event says: "sent", "none, no recipient", ...
	failed      bool
	lastAttempt time.Time
	gateReady   bool // the bus gate's answer at the last attempt
	logged      bool // the no-recipient line has been logged
}

// deliverHandoffMissing publishes the escalation when it is due and records
// what happened for the event. It never blocks longer than publishTimeout, never
// types into a pane, and never falls back to anything but the bus.
func (c *Controller) deliverHandoffMissing(t *api.Team, role *api.Role, sess api.Session, now time.Time) {
	if c.publishRecords == nil {
		c.publishRecords = make(map[string]*publishRecord)
	}
	rec := c.publishRecords[sess.Key()]
	if rec == nil {
		rec = &publishRecord{}
		c.publishRecords[sess.Key()] = rec
	}
	req := t.ShiftRequests[role.Name]

	switch {
	case c.Publisher == nil:
		rec.outcome = "none, no managed bus"
		return
	case role.Name == supervisorRoleName:
		// The role address would reach the escalated seat's own durable. The
		// other supervisors and the director are a follow-up.
		rec.outcome = "none, the escalated seat is itself a supervisor"
		return
	case !addressable.MatchString(t.Workspace) || !addressable.MatchString(t.Name):
		rec.outcome = "none, team name not addressable"
		return
	}
	hasRole := false
	for i := range t.Roles {
		if t.Roles[i].Name == supervisorRoleName {
			hasRole = true
		}
	}
	if !hasRole {
		rec.outcome = "none, no recipient"
		if !rec.logged {
			rec.logged = true
			log.Printf("escalation has no recipient: team %s declares no %s role", t.Key(), supervisorRoleName)
		}
		return
	}

	holder := false
	for _, s := range c.store.ListSessionsByTeamRole(t.Workspace, t.Name, supervisorRoleName) {
		if s.State == api.SessionRunning {
			holder = true
		}
	}
	window := role.Shift.HandoffWindowOrDefault()
	gateReady := true
	if c.BusGate != nil {
		gateReady, _ = c.BusGate()
	}

	var due bool
	switch {
	case rec.failed:
		// A failed send is not counted. Try again at the next window, or as
		// soon as the bus reports ready after a not-ready attempt.
		due = now.Sub(rec.lastAttempt) >= window || (gateReady && !rec.gateReady)
	case req.DeliveredCount == 0:
		due = true
	case !holder:
		due = false // queued once; hold until a seat holds the role
	default:
		due = req.LastDeliveryQueued || now.Sub(req.LastDeliveredAt) >= window
	}
	if !due {
		if rec.outcome == "" || rec.outcome == "none, no recipient" || rec.outcome == "none, no managed bus" {
			rec.outcome = persistedOutcome(req)
		}
		return
	}

	env, msgID := handoffMissingEnvelope(t, role, sess, req, now)
	ctx, cancel := context.WithTimeout(context.Background(), publishTimeout)
	err := c.Publisher.PublishRoleInbox(ctx, t.Workspace, t.Name, supervisorRoleName, env, msgID)
	cancel()
	rec.lastAttempt, rec.gateReady = now, gateReady
	if err != nil {
		rec.outcome = fmt.Sprintf("failed (%v)", err)
		if !rec.failed {
			events.Emit(c.Events, events.Event{
				Kind:       events.KindShiftHandoffUndelivered,
				Severity:   events.SeverityWarning,
				Workspace:  t.Workspace,
				Team:       t.Name,
				Role:       role.Name,
				Session:    sess.Key(),
				Generation: t.Generation,
				Message:    fmt.Sprintf("the handoff-missing escalation for %s could not be published to the %s role: %v; the event repeats, and marvel tries again at the next window or when the bus is ready", sess.Key(), supervisorRoleName, err),
			})
			log.Printf("shift: %s role %s: publish handoff-missing: %v", t.Key(), role.Name, err)
		}
		rec.failed = true
		return
	}
	rec.failed = false
	queued := !holder
	req.DeliveredCount++
	req.LastDeliveredAt = now
	req.LastDeliveryQueued = queued
	if err := c.store.UpdateTeam(t.Key(), func(live *api.Team) error {
		if live.ShiftRequests != nil {
			if cur, ok := live.ShiftRequests[role.Name]; ok && cur.Session == req.Session {
				cur.DeliveredCount, cur.LastDeliveredAt, cur.LastDeliveryQueued = req.DeliveredCount, req.LastDeliveredAt, req.LastDeliveryQueued
				live.ShiftRequests[role.Name] = cur
			}
		}
		return nil
	}); err != nil {
		log.Printf("shift: %s role %s: record handoff-missing send: %v", t.Key(), role.Name, err)
	}
	t.ShiftRequests[role.Name] = req
	rec.outcome = persistedOutcome(req)
}

// persistedOutcome is what the durable request says about its last send.
func persistedOutcome(req api.ShiftRequest) string {
	switch {
	case req.DeliveredCount == 0:
		return "not yet"
	case req.LastDeliveryQueued:
		return "queued once, no live holder"
	default:
		return "sent"
	}
}

// publishedClause is the event's account of the publish.
func (c *Controller) publishedClause(sessKey string) string {
	if rec := c.publishRecords[sessKey]; rec != nil && rec.outcome != "" {
		return "; published: " + rec.outcome
	}
	return ""
}

// causeClause says, in the envelope's text, why the seat was asked.
func causeClause(role *api.Role, req api.ShiftRequest) string {
	if req.Cause == api.ShiftTriggerContextPressure {
		return "had a context nearly full"
	}
	for _, cond := range role.Shift.Conditions() {
		if cond.On == api.ShiftTriggerMaxAge {
			return fmt.Sprintf("passed max age %s", cond.MaxAge)
		}
	}
	return "passed max age"
}

// handoffMissingEnvelope builds the REQUEST signal and its dedupe id (design
// H2 and H4). The correlation id is the same on every repeat; the message id
// is fresh; the dedupe id carries the count before this send, so a re-send
// after a crash between publish and record is dropped by the stream.
func handoffMissingEnvelope(t *api.Team, role *api.Role, sess api.Session, req api.ShiftRequest, now time.Time) (envelope.Envelope, string) {
	window := role.Shift.HandoffWindowOrDefault()
	corr := fmt.Sprintf("handoff-missing/%s/%s/%s/%d", t.Workspace, t.Name, sess.Name, req.RequestedAt.Unix())
	replyBy := now.Add(window)
	expires := replyBy.Add(window)
	text := fmt.Sprintf("handoff-missing: seat %s in %s role %s %s, asked %s; no handoff by %s. Decide: marvel shift %s --role %s, or leave it running.",
		sess.Key(), t.Key(), role.Name, causeClause(role, req), req.RequestedAt.UTC().Format(time.RFC3339),
		req.RequestedAt.Add(window).UTC().Format(time.RFC3339), t.Key(), role.Name)
	env := envelope.Envelope{
		SchemaVersion: 1,
		MessageID:     newULID(now),
		CorrelationID: &corr,
		Sender:        envelope.Sender{AgentID: escalationSender, Workspace: t.Workspace},
		Recipient:     envelope.Recipient{Address: fmt.Sprintf("role://%s/%s", t.Name, supervisorRoleName)},
		Performative:  envelope.PerformativeREQUEST,
		Authority:     &envelope.Authority{Strength: envelope.StrengthNone},
		Content:       envelope.Content{Type: envelope.TypeSignal, Data: text},
		SentAt:        now.UTC(),
		ReplyBy:       &replyBy,
		ExpiresAt:     &expires,
	}
	return env, fmt.Sprintf("%s/%d", corr, req.DeliveredCount)
}

// crockford is the ULID alphabet.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// newULID mints a ULID from the clock and crypto/rand: 48 bits of
// milliseconds, 80 bits of randomness, 26 characters.
func newULID(now time.Time) string {
	var b [16]byte
	ms := uint64(now.UnixMilli())
	for i := 0; i < 6; i++ {
		b[i] = byte(ms >> (8 * (5 - i)))
	}
	if _, err := rand.Read(b[6:]); err != nil {
		for i := 6; i < 16; i++ {
			b[i] = byte(ms >> (i % 8))
		}
	}
	out := make([]byte, 26)
	// 128 bits into 26 five-bit groups, most significant first (the first
	// group carries the top 3 bits).
	var acc uint
	var bits int
	idx := 25
	for i := 15; i >= 0; i-- {
		acc |= uint(b[i]) << bits
		bits += 8
		for bits >= 5 && idx >= 0 {
			out[idx] = crockford[acc&31]
			acc >>= 5
			bits -= 5
			idx--
		}
	}
	if idx >= 0 {
		out[idx] = crockford[acc&31]
	}
	return string(out)
}
