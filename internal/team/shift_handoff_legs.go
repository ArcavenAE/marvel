package team

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/arcavenae/marvel/contracts/go/envelope"
	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// The escalation legs for a supervisor seat that misses its handoff window and
// for a team with no supervisor role (docs/design/handoff-missing-delivery.md
// H1, marvel#1309 and marvel#1311). marvel states a fact and asks; it never
// reads a reply, never types into a pane, and never changes state because a
// notice was sent.

const (
	// directorAddress is the director's recipient address on the envelope.
	directorAddress = "global://director"
	// directorSendCap is how many notices the director gets per request: one at
	// escalation and one repeat when the request is reported stuck. Nothing
	// follows, however long the request stays stuck.
	directorSendCap = 2
)

// directorLeg publishes the escalation to the director when it is due and says
// what it did, for the event. It is on only while the leaf is up: with no hub
// there is no director leg, and the escalation is event-only as before. The
// leaf is the only thing marvel can read, so "sent" means published, never read.
//
// The first notice goes at the first tick the leg is on. The second goes only
// after the request is reported stuck (team.shift-request-stuck) following the
// first, and nothing goes after the second. A send that fails is not counted.
func (c *Controller) directorLeg(t *api.Team, role *api.Role, sess api.Session, req *api.ShiftRequest, now time.Time, rec *publishRecord) string {
	if c.LeafUp == nil || !c.LeafUp() {
		return "director off (leaf not up)"
	}
	label := fmt.Sprintf("director sent %d of %d", req.DirectorSent, directorSendCap)
	switch {
	case req.DirectorSent >= directorSendCap:
		return label
	case req.DirectorSent == 1 && !c.stuckAt[sess.Key()].After(req.DirectorSentAt):
		return label
	case rec.dirFailed && now.Sub(rec.dirLastAttempt) < role.Shift.HandoffWindowOrDefault():
		return fmt.Sprintf("director failed, %d of %d sent", req.DirectorSent, directorSendCap)
	}

	env, _ := handoffMissingEnvelope(t, role, sess, *req, now)
	env.Recipient.Address = directorAddress
	msgID := fmt.Sprintf("%s/director/%d", *env.CorrelationID, req.DirectorSent)
	ctx, cancel := context.WithTimeout(context.Background(), publishTimeout)
	err := c.Publisher.PublishDirectorInbox(ctx, env, msgID)
	cancel()
	rec.dirLastAttempt = now
	if err != nil {
		if !rec.dirFailed {
			c.emitUndelivered(t, role, sess, "the director", err)
		}
		rec.dirFailed = true
		return fmt.Sprintf("director failed (%v), %d of %d sent", err, req.DirectorSent, directorSendCap)
	}
	rec.dirFailed = false
	req.DirectorSent++
	req.DirectorSentAt = now
	c.persistRequest(t, role, *req)
	return fmt.Sprintf("director sent %d of %d", req.DirectorSent, directorSendCap)
}

// persistRequest writes the delivery fields of req to the stored request, if it
// is still the same request, and to the caller's copy of the team.
func (c *Controller) persistRequest(t *api.Team, role *api.Role, req api.ShiftRequest) {
	if err := c.store.UpdateTeam(t.Key(), func(live *api.Team) error {
		if cur, ok := live.ShiftRequests[role.Name]; ok && cur.Session == req.Session {
			cur.DeliveredCount, cur.LastDeliveredAt, cur.LastDeliveryQueued = req.DeliveredCount, req.LastDeliveredAt, req.LastDeliveryQueued
			cur.DirectorSent, cur.DirectorSentAt = req.DirectorSent, req.DirectorSentAt
			live.ShiftRequests[role.Name] = cur
		}
		return nil
	}); err != nil {
		log.Printf("shift: %s role %s: record handoff-missing send: %v", t.Key(), role.Name, err)
	}
	if t.ShiftRequests != nil {
		t.ShiftRequests[role.Name] = req
	}
}

// emitUndelivered says once per transition that a leg's publish failed.
func (c *Controller) emitUndelivered(t *api.Team, role *api.Role, sess api.Session, to string, err error) {
	events.Emit(c.Events, events.Event{
		Kind:       events.KindShiftHandoffUndelivered,
		Severity:   events.SeverityWarning,
		Workspace:  t.Workspace,
		Team:       t.Name,
		Role:       role.Name,
		Session:    sess.Key(),
		Generation: t.Generation,
		Message:    fmt.Sprintf("the handoff-missing escalation for %s could not be published to %s: %v; the event repeats, and marvel tries again at the next window or when the bus is ready", sess.Key(), to, err),
	})
	log.Printf("shift: %s role %s: publish handoff-missing to %s: %v", t.Key(), role.Name, to, err)
}

// deliverFromSupervisor is the escalation of a seat that is itself a
// supervisor. The role address would reach its own durable, so it goes to:
// every other live replica of the same role, by agent address (the seat's own
// inbox, which its shim always reads); the supervisor role of every other team
// in the same workspace that has a running one, by role address; and the
// director, when the leaf is up. The first two repeat on the normal cadence;
// the director keeps its cap. Supervisors on other clusters are not included.
func (c *Controller) deliverFromSupervisor(t *api.Team, role *api.Role, sess api.Session, req api.ShiftRequest, now time.Time, rec *publishRecord) {
	var replicas []api.Session
	for _, s := range c.store.ListSessionsByTeamRole(t.Workspace, t.Name, supervisorRoleName) {
		if s.State == api.SessionRunning && s.Key() != sess.Key() && addressable.MatchString(s.Name) {
			replicas = append(replicas, s)
		}
	}
	var peers []string
	for _, ot := range c.store.ListTeams() {
		if ot.Workspace != t.Workspace || ot.Name == t.Name || !addressable.MatchString(ot.Name) || !hasSupervisorRole(&ot) {
			continue
		}
		for _, s := range c.store.ListSessionsByTeamRole(ot.Workspace, ot.Name, supervisorRoleName) {
			if s.State == api.SessionRunning {
				peers = append(peers, ot.Name)
				break
			}
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
		due = now.Sub(rec.lastAttempt) >= window || (gateReady && !rec.gateReady)
	case req.DeliveredCount == 0:
		due = true
	default:
		due = now.Sub(req.LastDeliveredAt) >= role.Shift.HandoffRepeatGap(req.DeliveredCount)
	}

	var parts []string
	if due && (len(replicas) > 0 || len(peers) > 0) {
		corr := ""
		sent, failed := 0, 0
		var firstErr error
		try := func(publish func(ctx context.Context, env envelope.Envelope, id string) error, address, idPart string) (int, int) {
			env, _ := handoffMissingEnvelope(t, role, sess, req, now)
			env.Recipient.Address = address
			corr = *env.CorrelationID
			id := fmt.Sprintf("%s/%s/%d", corr, idPart, req.DeliveredCount)
			ctx, cancel := context.WithTimeout(context.Background(), publishTimeout)
			err := publish(ctx, env, id)
			cancel()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return 0, 1
			}
			return 1, 0
		}
		rs, rf := 0, 0
		for _, s := range replicas {
			s := s
			a, b := try(func(ctx context.Context, env envelope.Envelope, id string) error {
				return c.Publisher.PublishSeatInbox(ctx, t.Workspace, t.Name, s.Name, env, id)
			}, fmt.Sprintf("agent://%s/%s", t.Name, s.Name), "seat/"+s.Name)
			rs, rf = rs+a, rf+b
		}
		ps, pf := 0, 0
		for _, team := range peers {
			team := team
			a, b := try(func(ctx context.Context, env envelope.Envelope, id string) error {
				return c.Publisher.PublishRoleInbox(ctx, t.Workspace, team, supervisorRoleName, env, id)
			}, fmt.Sprintf("role://%s/%s", team, supervisorRoleName), "role/"+team)
			ps, pf = ps+a, pf+b
		}
		sent, failed = rs+ps, rf+pf
		rec.lastAttempt, rec.gateReady = now, gateReady
		if failed > 0 {
			if !rec.failed {
				c.emitUndelivered(t, role, sess, "the other supervisors", firstErr)
			}
			rec.failed = true
		} else {
			rec.failed = false
		}
		if sent > 0 {
			req.DeliveredCount++
			req.LastDeliveredAt = now
			req.LastDeliveryQueued = false
			c.persistRequest(t, role, req)
		}
		rec.legs = legSummary(len(replicas), rs, rf, len(peers), ps, pf)
	} else if len(replicas) == 0 && len(peers) == 0 {
		rec.legs = ""
	}
	if rec.legs != "" {
		parts = append(parts, rec.legs)
	}

	dir := c.directorLeg(t, role, sess, &req, now, rec)
	switch {
	case len(replicas) == 0 && len(peers) == 0 && req.DirectorSent == 0 && !c.directorOn():
		rec.outcome = "none, no other supervisor and no global tier (" + dir + ")"
	default:
		rec.outcome = joinLegs(append(parts, dir))
	}
}

// directorOn reports whether the director leg is on at this moment.
func (c *Controller) directorOn() bool { return c.LeafUp != nil && c.LeafUp() }

// legSummary words the replica and other-team legs of the last attempt.
func legSummary(replicas, rs, rf, peers, ps, pf int) string {
	var parts []string
	if replicas > 0 {
		parts = append(parts, legWords("replicas", rs, rf))
	}
	if peers > 0 {
		parts = append(parts, legWords("other teams", ps, pf))
	}
	return joinLegs(parts)
}

func legWords(name string, sent, failed int) string {
	if failed > 0 {
		return fmt.Sprintf("%s sent %d, failed %d", name, sent, failed)
	}
	return fmt.Sprintf("%s sent %d", name, sent)
}

func joinLegs(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}
