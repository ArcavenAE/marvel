package team

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// Hibernate and wake (docs/design/fleet-hibernate.md sections 3, 4, 6 and 7),
// handoff mode only: every seat is asked for its handoff, ended on its marker
// with no successor, and comes back as a fresh seat on wake.

// hibernateNotice is what a seat is sent when its team hibernates. Everything
// from "write your handoff now" on is built by prepareHandoffNotice, as the
// shift and retire notices are.
const hibernateNotice = "hibernate: write your handoff now"

// wakeHealthFallback is how long a woken supervisor has to heartbeat when its
// role declares no health timeout, before the workers start anyway.
const wakeHealthFallback = 5 * time.Minute

// HibernateOptions is who asks and what a hibernate covers.
type HibernateOptions struct {
	Actor, Source string
	// Roles are the roles to hibernate; empty means the whole team.
	Roles []string
}

// HibernateReport is what a hibernate call found: the roles now covered and how
// many running seats it will sign off.
type HibernateReport struct {
	Team  string
	Roles []string
	Seats int
}

// ErrHibernateRefused wraps the refusals a hibernate or wake gives.
var ErrHibernateRefused = errors.New("hibernate refused")

// runningSeats returns the live seats of role that a hibernate can ask: running,
// interactive and not already being retired. A headless seat is left alone, as
// a retirement leaves it (operator ruling 2026-10-28).
func (c *Controller) runningSeats(t *api.Team, role *api.Role) []api.Session {
	var out []api.Session
	for _, s := range c.store.ListSessionsByTeamRole(t.Workspace, t.Name, role.Name) {
		if s.State != api.SessionRunning || isRetiring(t, s.Key()) || role.Runtime.Mode == api.RuntimeModeHeadless {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}

// Hibernate starts, or extends, a hibernate of teamKey. It marks the team at
// once, drops any open max-age or context-pressure request on a covered role
// (the hibernate supersedes it), and returns; the sign-off itself runs on the
// reconcile ticks. It refuses a team that is mid-shift or being pruned.
func (c *Controller) Hibernate(teamKey string, opt HibernateOptions) (HibernateReport, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, err := c.store.GetTeam(teamKey)
	if err != nil {
		return HibernateReport{}, err
	}
	switch {
	case t.Shift.Phase != api.ShiftNone:
		return HibernateReport{}, fmt.Errorf("%w: %s is mid-shift (%s); wait for it to finish", ErrHibernateRefused, teamKey, t.Shift.Phase)
	case t.Pruning != nil:
		return HibernateReport{}, fmt.Errorf("%w: %s is being pruned", ErrHibernateRefused, teamKey)
	}
	for _, name := range opt.Roles {
		if roleOf(&t, name) == nil {
			return HibernateReport{}, fmt.Errorf("%w: team %s has no role %q", ErrHibernateRefused, teamKey, name)
		}
	}
	now := c.nowUTC()
	if err := c.store.UpdateTeam(teamKey, func(live *api.Team) error {
		roles := api.HibernateRolesAfter(live.Hibernate, opt.Roles)
		if live.Hibernate == nil || live.Hibernate.State != api.HibernateHibernated {
			live.Hibernate = &api.HibernateMark{State: api.HibernateHibernated, Since: now, Actor: opt.Actor, Source: opt.Source, Mode: api.HibernateModeHandoff}
		}
		live.Hibernate.Roles = roles
		return nil
	}); err != nil {
		return HibernateReport{}, fmt.Errorf("mark %s hibernated: %w", teamKey, err)
	}
	t, _ = c.store.GetTeam(teamKey)
	rep := HibernateReport{Team: teamKey}
	for i := range t.Roles {
		role := &t.Roles[i]
		if !t.Hibernate.Covers(role.Name) {
			continue
		}
		rep.Roles = append(rep.Roles, role.Name)
		rep.Seats += len(c.runningSeats(&t, role))
		if req, ok := t.ShiftRequests[role.Name]; ok {
			log.Printf("hibernate: %s superseded the open %s handoff request on role %s", teamKey, req.Cause, role.Name)
			c.dropShiftRequest(&t, role.Name)
		}
	}
	return rep, nil
}

// advanceHibernate moves a hibernate or a wake on one step. Caller holds c.mu.
func (c *Controller) advanceHibernate(t *api.Team, now time.Time) {
	switch {
	case t.Hibernate == nil:
		return
	case t.Hibernate.State == api.HibernateWaking:
		c.advanceWake(t, now)
	default:
		c.advanceSignOff(t, now)
	}
}

// roleSeatsOf is the covered roles of the team, workers first and supervisors
// last, each with its running seats.
func (c *Controller) coveredRoles(t *api.Team) (workers, supervisors []*api.Role) {
	for i := range t.Roles {
		role := &t.Roles[i]
		if !t.Hibernate.Covers(role.Name) {
			continue
		}
		if api.IsSupervisorRole(*role) {
			supervisors = append(supervisors, role)
		} else {
			workers = append(workers, role)
		}
	}
	return workers, supervisors
}

// withShift returns role with a non-nil Shift, so the escalation helpers that
// read the handoff declaration directly never see a nil policy.
func withShift(role *api.Role) *api.Role {
	if role.Shift != nil {
		return role
	}
	r := *role
	r.Shift = &api.ShiftPolicy{}
	return &r
}

// advanceSignOff drives the sign-off: advance the open requests, ask the worker
// tier all at once, ask the supervisors once every worker has ended or been
// reported, and say so when nothing is left. Caller holds c.mu.
func (c *Controller) advanceSignOff(t *api.Team, now time.Time) {
	keys := make([]string, 0, len(t.HibernateRequests))
	for k := range t.HibernateRequests {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		c.advanceHibernateRequest(t, t.HibernateRequests[k], now)
	}

	workers, supervisors := c.coveredRoles(t)
	for _, role := range workers {
		for _, s := range c.runningSeats(t, role) {
			c.askHibernate(t, role, s, now, "")
		}
	}
	if !c.workersSettled(t, workers) {
		return
	}
	var reported []string
	for _, k := range keys {
		if r, ok := t.HibernateRequests[k]; ok && r.Escalated {
			reported = append(reported, k)
		}
	}
	extra := ""
	if len(reported) > 0 {
		extra = fmt.Sprintf(" (still running with no handoff: %s; record that in yours)", strings.Join(reported, ", "))
	}
	for _, role := range supervisors {
		for _, s := range c.runningSeats(t, role) {
			c.askHibernate(t, role, s, now, extra)
		}
	}
	c.noteHibernated(t, now)
}

// workersSettled reports whether every running worker seat of the covered roles
// has been asked and has either ended or been reported with no handoff.
func (c *Controller) workersSettled(t *api.Team, workers []*api.Role) bool {
	for _, role := range workers {
		for _, s := range c.runningSeats(t, role) {
			req, ok := t.HibernateRequests[s.Key()]
			if !ok || !req.Escalated {
				return false
			}
		}
	}
	return true
}

// askHibernate asks sess for its handoff, once. A seat that is mid-turn waits
// for a pause of its quiet_for, and is asked whatever it is doing once its
// max_defer has passed since the hibernate began (design section 4, timeouts).
func (c *Controller) askHibernate(t *api.Team, role *api.Role, sess api.Session, now time.Time, extra string) {
	if _, ok := t.HibernateRequests[sess.Key()]; ok {
		return
	}
	quietFor, maxDefer := api.DefaultShiftAskPause, api.DefaultShiftMaxDefer
	for _, cond := range role.Shift.Conditions() {
		if cond.On != api.ShiftTriggerMaxAge {
			continue
		}
		if cond.QuietFor > 0 {
			quietFor = cond.QuietFor
		}
		if cond.MaxDefer > 0 {
			maxDefer = cond.MaxDefer
		}
	}
	if now.Sub(lastWorked(sess)) < quietFor && now.Sub(t.Hibernate.Since) < maxDefer {
		return
	}
	rr := withShift(role)
	prep := prepareHandoffNotice(hibernateNotice+extra, rr.Shift.Handoff, rr.Shift.HandoffMarker, rr.Shift.HandoffWindowOrDefault(), sess)
	if prep.heldDir != nil {
		_ = prep.heldDir.Close()
	}
	undelivered := ""
	if c.Notify == nil {
		undelivered = "no notifier"
	} else if err := c.Notify(sess, prep.text, NoticeHibernate); err != nil {
		undelivered = err.Error()
	}
	req := api.ShiftRequest{Session: sess.Key(), Cause: api.HandoffCauseHibernate, RequestedAt: now,
		NoticeUndelivered: undelivered, DirError: prep.dirError, DirDev: prep.dirDev, DirIno: prep.dirIno}
	if err := c.putHibernateRequest(t, req); err != nil {
		log.Printf("hibernate: %s: record request for %s: %v", t.Key(), sess.Key(), err)
		return
	}
	delivery := "delivered"
	if undelivered != "" {
		delivery = fmt.Sprintf("not delivered (%s)", undelivered)
	}
	events.Emit(c.Events, events.Event{
		Kind: events.KindShiftHandoffRequested, Severity: events.SeverityInfo,
		Workspace: t.Workspace, Team: t.Name, Role: role.Name, Session: sess.Key(), Generation: t.Generation,
		Message: fmt.Sprintf("cause=%s: hibernate by %s; notice %s; handoff window %s", api.HandoffCauseHibernate, t.Hibernate.Actor, delivery, rr.Shift.HandoffWindowOrDefault()),
	})
}

func (c *Controller) putHibernateRequest(t *api.Team, req api.ShiftRequest) error {
	if err := c.store.UpdateTeam(t.Key(), func(live *api.Team) error {
		if live.HibernateRequests == nil {
			live.HibernateRequests = make(map[string]api.ShiftRequest)
		}
		live.HibernateRequests[req.Session] = req
		return nil
	}); err != nil {
		return err
	}
	if t.HibernateRequests == nil {
		t.HibernateRequests = make(map[string]api.ShiftRequest)
	}
	t.HibernateRequests[req.Session] = req
	return nil
}

// dropHibernateRequest ends a request's record and forgets what the escalation
// helpers kept for its seat.
func (c *Controller) dropHibernateRequest(t *api.Team, session string) {
	c.handoffProbes.forget(session)
	delete(c.missingEmitted, session)
	delete(c.stuckEmitted, session)
	delete(c.publishRecords, session)
	if err := c.store.UpdateTeam(t.Key(), func(live *api.Team) error {
		delete(live.HibernateRequests, session)
		return nil
	}); err != nil && !errors.Is(err, api.ErrNotFound) {
		log.Printf("hibernate: %s: drop request for %s: %v", t.Key(), session, err)
		return
	}
	delete(t.HibernateRequests, session)
}

// advanceHibernateRequest moves one request on: end the seat on its marker,
// escalate once the window passes with none, and keep saying so each window. It
// never ends a seat that wrote no handoff (the succession contract).
func (c *Controller) advanceHibernateRequest(t *api.Team, req api.ShiftRequest, now time.Time) {
	sess, err := c.store.GetSession(req.Session)
	if err != nil || !sess.State.CountsAsAlive() || !sess.CreatedAt.Before(req.RequestedAt) {
		c.dropHibernateRequest(t, req.Session)
		return
	}
	role := roleOf(t, sess.Role)
	if role == nil {
		c.dropHibernateRequest(t, req.Session)
		return
	}
	rr := withShift(role)
	c.noteStuckRequest(t, rr, req, sess, now)
	if req.Escalated {
		c.deliverMissingFor(t, rr, sess, req, now, func(r api.ShiftRequest) { _ = c.putHibernateRequest(t, r) })
		c.repeatHandoffMissing(t, rr, sess, t.HibernateRequests[req.Session], now)
		return
	}
	path, pathErr := "", error(nil)
	if rr.Shift.Handoff != "" {
		path, pathErr = handoffPath(rr.Shift.Handoff, sess)
	}
	var read handoffProbeResult
	var haveRead bool
	if path != "" && pathErr == nil {
		read, haveRead = c.handoffProbes.complete(sess.Key(), path, rr.Shift.HandoffMarker, now)
		if haveRead && read.complete {
			c.endHibernated(t, sess, retireHandoffWritten, fmt.Sprintf("hibernate: %s ends in the marker", path))
			return
		}
	}
	window := rr.Shift.HandoffWindowOrDefault()
	if now.Sub(req.RequestedAt) < window {
		return
	}
	if path != "" && pathErr == nil && (!haveRead || read.startedAt.Before(req.RequestedAt.Add(window))) {
		return
	}
	req.Escalated = true
	if err := c.putHibernateRequest(t, req); err != nil {
		log.Printf("hibernate: %s: record escalation for %s: %v", t.Key(), sess.Key(), err)
		return
	}
	c.deliverMissingFor(t, rr, sess, req, now, func(r api.ShiftRequest) { _ = c.putHibernateRequest(t, r) })
	c.emitHandoffMissing(t, rr, sess, t.HibernateRequests[req.Session], now, false, false)
}

// endHibernated saves the seat's screen, records the outcome and ends the seat
// with the same delete a retirement uses: no successor is launched, and the
// hibernate mark stops the replica slot from being refilled.
func (c *Controller) endHibernated(t *api.Team, sess api.Session, outcome, reason string) {
	saved := c.saveRetiredScreen(sess)
	c.emitRetired(t, sess, outcome, reason, saved)
	if err := c.sessMgr.DeleteAndReap(sess.Key()); err != nil {
		log.Printf("hibernate: delete session %s: %v", sess.Key(), err)
	}
	c.dropHibernateRequest(t, sess.Key())
}

// HibernateEnd ends sess without a handoff, the one explicit act that loses a
// seat's context. It never runs by itself and only on a seat the mark covers.
func (c *Controller) HibernateEnd(teamKey, session string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, err := c.store.GetTeam(teamKey)
	if err != nil {
		return err
	}
	sess, err := c.store.GetSession(session)
	if err != nil || sess.Workspace != t.Workspace || sess.Team != t.Name {
		return fmt.Errorf("%w: %s is not a seat of %s", ErrHibernateRefused, session, teamKey)
	}
	if t.Hibernate == nil || t.Hibernate.State != api.HibernateHibernated || !t.Hibernate.Covers(sess.Role) {
		return fmt.Errorf("%w: %s is not covered by a hibernate of %s", ErrHibernateRefused, session, teamKey)
	}
	if !sess.State.CountsAsAlive() {
		return fmt.Errorf("%w: %s is not running", ErrHibernateRefused, session)
	}
	c.endHibernated(&t, sess, retireHandoffMissing, "ended by the operator with no handoff (hibernate --end)")
	return nil
}

// noteHibernated says the team is hibernated once nothing is left running and
// no request is open. The record of having said so is in memory, so a daemon
// restart says it again.
func (c *Controller) noteHibernated(t *api.Team, now time.Time) {
	if len(t.HibernateRequests) > 0 {
		return
	}
	for i := range t.Roles {
		role := &t.Roles[i]
		if t.Hibernate.Covers(role.Name) && api.CountAlive(c.store.ListSessionsByTeamRole(t.Workspace, t.Name, role.Name)) > 0 {
			return
		}
	}
	if c.hibernateSaid == nil {
		c.hibernateSaid = make(map[string]time.Time)
	}
	if at, ok := c.hibernateSaid[t.Key()]; ok && at.Equal(t.Hibernate.Since) {
		return
	}
	c.hibernateSaid[t.Key()] = t.Hibernate.Since
	roles := "every role"
	if len(t.Hibernate.Roles) > 0 {
		roles = "roles " + strings.Join(t.Hibernate.Roles, ", ")
	}
	events.Emit(c.Events, events.Event{
		Kind: events.KindTeamHibernated, Severity: events.SeverityInfo,
		Workspace: t.Workspace, Team: t.Name, Generation: t.Generation,
		Message: fmt.Sprintf("hibernated by %s via %s since %s: %s; nothing spawns for the team until marvel wake", t.Hibernate.Actor, t.Hibernate.Source, t.Hibernate.Since.Format(time.RFC3339), roles),
	})
}

// hibernateBlocks reports whether the team's mark stops a spawn of role now, and
// why. A hibernated mark stops every spawn it covers. A waking mark lets the
// supervisor roles start, and the others once a supervisor has heartbeated or
// its health timeout has passed. Caller holds c.mu.
func (c *Controller) hibernateBlocks(t *api.Team, role *api.Role) (bool, string) {
	m := t.Hibernate
	if m == nil || !m.Covers(role.Name) {
		return false, ""
	}
	if m.State != api.HibernateWaking {
		return true, "hibernated; run marvel wake"
	}
	if api.IsSupervisorRole(*role) || c.wakeAllowsWorkers(t, c.nowUTC()) {
		return false, ""
	}
	return true, "waking: waiting for a supervisor to heartbeat"
}

// wakeAllowsWorkers reports whether a waking team's non-supervisor roles may
// start: it has no covered supervisor role, or a supervisor seat has
// heartbeated since the wake began, or the supervisor's health timeout passed.
func (c *Controller) wakeAllowsWorkers(t *api.Team, now time.Time) bool {
	_, supervisors := c.coveredRoles(t)
	if len(supervisors) == 0 {
		return true
	}
	for _, role := range supervisors {
		for _, s := range c.store.ListSessionsByTeamRole(t.Workspace, t.Name, role.Name) {
			if s.State.CountsAsAlive() && s.LastHeartbeat.After(t.Hibernate.Since) {
				return true
			}
		}
		timeout := time.Duration(0)
		if role.HealthCheck != nil {
			timeout = role.HealthCheck.Timeout
		}
		if timeout <= 0 {
			timeout = wakeHealthFallback
		}
		if now.Sub(t.Hibernate.Since) >= timeout {
			return true
		}
	}
	return false
}

// WakeOptions is who asks and which preflight checks the operator skipped.
type WakeOptions struct {
	Actor, Source string
	SkippedChecks []string
}

// Wake brings a hibernated or waking team back (design section 6). The caller
// has run the preflight. It sets the convergence posture to converge, moves the
// mark to waking, and drops the sign-off requests. It is safe to run again on a
// waking team: a restart that reset the team to hold while the mark read
// waking is undone, the roles already started are counted and not started
// twice, and the wake goes on with the rest.
func (c *Controller) Wake(teamKey string, opt WakeOptions) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, err := c.store.GetTeam(teamKey)
	if err != nil {
		return err
	}
	if t.Hibernate == nil {
		return fmt.Errorf("%w: %s is not hibernated", ErrHibernateRefused, teamKey)
	}
	now := c.nowUTC()
	if err := c.store.UpdateTeam(teamKey, func(live *api.Team) error {
		live.ConvergencePosture = api.PostureConverge
		live.Hibernate.State = api.HibernateWaking
		live.Hibernate.Since = now
		live.Hibernate.Actor = opt.Actor
		live.Hibernate.Source = opt.Source
		return nil
	}); err != nil {
		return fmt.Errorf("wake %s: %w", teamKey, err)
	}
	t, _ = c.store.GetTeam(teamKey)
	for session := range t.HibernateRequests {
		c.dropHibernateRequest(&t, session)
	}
	msg := fmt.Sprintf("wake by %s via %s: posture converge, supervisor roles may start", opt.Actor, opt.Source)
	if len(opt.SkippedChecks) > 0 {
		msg += "; skipped checks: " + strings.Join(opt.SkippedChecks, ", ")
	}
	events.Emit(c.Events, events.Event{
		Kind: events.KindTeamWaking, Severity: events.SeverityInfo,
		Workspace: t.Workspace, Team: t.Name, Generation: t.Generation, Message: msg,
	})
	return nil
}

// advanceWake clears the mark once every covered role has its replicas started,
// and says what the wake did. Caller holds c.mu.
func (c *Controller) advanceWake(t *api.Team, now time.Time) {
	var notUp []string
	for i := range t.Roles {
		role := &t.Roles[i]
		if !t.Hibernate.Covers(role.Name) {
			continue
		}
		alive := api.CountAlive(c.store.ListSessionsByTeamRole(t.Workspace, t.Name, role.Name))
		if alive < t.DesiredReplicas(role) {
			return
		}
	}
	_, supervisors := c.coveredRoles(t)
	for _, role := range supervisors {
		up := false
		for _, s := range c.store.ListSessionsByTeamRole(t.Workspace, t.Name, role.Name) {
			if s.State.CountsAsAlive() && s.LastHeartbeat.After(t.Hibernate.Since) {
				up = true
			}
		}
		if !up {
			notUp = append(notUp, role.Name)
		}
	}
	if err := c.store.UpdateTeam(t.Key(), func(live *api.Team) error {
		live.Hibernate = nil
		return nil
	}); err != nil {
		log.Printf("wake: %s: clear the mark: %v", t.Key(), err)
		return
	}
	msg := "every covered role has its seats; all started fresh from their handoffs"
	sev := events.SeverityInfo
	if len(notUp) > 0 {
		msg += "; supervisor not up by its health timeout: " + strings.Join(notUp, ", ")
		sev = events.SeverityWarning
	}
	delete(c.hibernateSaid, t.Key())
	events.Emit(c.Events, events.Event{
		Kind: events.KindTeamWoken, Severity: sev,
		Workspace: t.Workspace, Team: t.Name, Generation: t.Generation, Message: msg,
	})
	t.Hibernate = nil
}

// hibernateRequestFor is the open hibernate request of session, if any.
func hibernateRequestFor(t *api.Team, session string) (api.ShiftRequest, bool) {
	req, ok := t.HibernateRequests[session]
	return req, ok
}

// recordHibernatePut stamps the receipt of a handoff.put on session's request.
func recordHibernatePut(live *api.Team, session string, now time.Time) {
	if r, ok := live.HibernateRequests[session]; ok {
		r.PutAt = now
		live.HibernateRequests[session] = r
	}
}

// planHibernatePut is planHandoffPutLocked for a seat holding a hibernate
// request: a seat that cannot write its own file answers the notice through the
// daemon, to the file the notice named.
func (c *Controller) planHibernatePut(token, text string, t *api.Team, sess api.Session, req api.ShiftRequest, plan handoffPutPlan) (handoffPutPlan, error) {
	if _, err := c.liveSessionForPut(token, &req); err != nil {
		return plan, err
	}
	role := roleOf(t, sess.Role)
	if role == nil {
		return plan, fmt.Errorf("%w: role %s is not in team %s", ErrHandoffNotRequested, sess.Role, t.Key())
	}
	if role.Shift == nil || role.Shift.Handoff == "" || role.Shift.HandoffMarker == "" {
		return plan, fmt.Errorf("%w: role %s", ErrHandoffNoPath, role.Name)
	}
	path, err := handoffPath(role.Shift.Handoff, sess)
	if err != nil {
		return plan, fmt.Errorf("%w: %v", ErrHandoffNoPath, err)
	}
	plan.teamKey, plan.role, plan.generation, plan.req, plan.hibernate = t.Key(), role.Name, t.Generation, req, true
	plan.path, plan.body = path, handoffBody(text, role.Shift.HandoffMarker)
	return plan, nil
}

// recheckHibernatePut confirms, after the write, that the same hibernate
// request still stands and its seat is still the live one.
func (c *Controller) recheckHibernatePut(token string, t *api.Team, plan handoffPutPlan) error {
	req, ok := t.HibernateRequests[plan.sess.Key()]
	if !ok || !req.RequestedAt.Equal(plan.req.RequestedAt) || req.Escalated {
		return fmt.Errorf("%w: the hibernate request for %s changed while the handoff was written", ErrHandoffNotRequested, plan.sess.Key())
	}
	_, err := c.liveSessionForPut(token, &req)
	return err
}

// wakePredecessors returns, newest first, the keys of the seats of role that a
// wake in progress replaces: the resume journal's entries for the role whose
// seat is not alive now. A new seat takes one as its predecessor, so the first
// prompt names the handoff the hibernated seat wrote. Nil unless the team is
// waking.
func (c *Controller) wakePredecessors(t *api.Team, role string) []string {
	if t.Hibernate == nil || t.Hibernate.State != api.HibernateWaking {
		return nil
	}
	entries, err := c.store.ResumeJournal()
	if err != nil {
		log.Printf("wake: %s: read the resume journal: %v", t.Key(), err)
		return nil
	}
	var picked []api.ResumeEntry
	for _, e := range entries {
		if e.Workspace != t.Workspace || e.Team != t.Name || e.Role != role {
			continue
		}
		if s, err := c.store.GetSession(e.Session); err == nil && s.State.CountsAsAlive() {
			continue
		}
		picked = append(picked, e)
	}
	sort.SliceStable(picked, func(i, j int) bool { return picked[i].LaunchedAt.After(picked[j].LaunchedAt) })
	out := make([]string, len(picked))
	for i, e := range picked {
		out[i] = e.Session
	}
	return out
}
