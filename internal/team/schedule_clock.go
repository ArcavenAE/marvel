package team

import (
	"fmt"
	"log"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// scheduleGrace is how late a firing may start and still be on time when
// the schedule sets no starting_deadline. The reconciler ticks every two
// seconds, so a minute absorbs a slow tick; anything later, such as a
// daemon that was down, is a missed firing, which an unset
// starting_deadline does not catch up.
const scheduleGrace = time.Minute

// maxCatchUpScan bounds the count of missed due times walked on recovery.
// A minutely schedule down for two months stays under it.
const maxCatchUpScan = 100_000

// reconcileScheduleClock is the schedule clock (scheduled-runs section 2):
// for each scheduled role it computes the next due time, and when one
// arrives it advances the role's firing in the store. It never spawns. The
// reconciler sees the new firing in planRole, on this same tick, and
// spawns through the path every role uses. The only session it touches is
// a previous run that concurrency = replace kills.
func (c *Controller) reconcileScheduleClock(teams []api.Team) {
	now := c.nowUTC()
	for i := range teams {
		t := &teams[i]
		for j := range t.Roles {
			if t.Roles[j].Schedule != nil {
				c.stepScheduleClock(t, &t.Roles[j], now)
			}
		}
	}
}

// clockStep is what one step of the clock decided, applied after the
// store update so nothing calls back into the store under its lock.
type clockStep struct {
	events []events.Event
	kill   []string
}

func (c *Controller) stepScheduleClock(t *api.Team, role *api.Role, now time.Time) {
	key := t.Workspace + "/" + t.Name + "/" + role.Name
	var live []string
	for _, s := range c.store.ListSessionsByTeamRole(t.Workspace, t.Name, role.Name) {
		if s.State.CountsAsAlive() {
			live = append(live, s.Key())
		}
	}
	withheld := c.postureWithholds(t)

	var step clockStep
	if _, err := c.store.UpdateScheduleStatus(key, func(st *api.ScheduleStatus) bool {
		step = clockStep{}
		return c.advanceFiring(t, role, st, now, live, withheld, &step)
	}); err != nil {
		log.Printf("warning: schedule clock %s: %v", key, err)
		return
	}
	for _, k := range step.kill {
		if err := c.sessMgr.Delete(k); err != nil {
			log.Printf("schedule: replace %s: %v", k, err)
		}
	}
	for _, ev := range step.events {
		events.Emit(c.Events, ev)
	}
}

func (c *Controller) drawJitter(limit time.Duration) time.Duration {
	if limit <= 0 {
		return 0
	}
	if c.jitter != nil {
		return c.jitter(limit)
	}
	return rand.N(limit)
}

// advanceFiring is one step of the clock against the stored status. It
// reports whether it changed the status.
func (c *Controller) advanceFiring(t *api.Team, role *api.Role, st *api.ScheduleStatus, now time.Time, live []string, withheld bool, step *clockStep) bool {
	p := *role.Schedule
	event := func(kind events.Kind, sev events.Severity, format string, args ...any) {
		step.events = append(step.events, events.Event{
			Kind: kind, Severity: sev,
			Workspace: t.Workspace, Team: t.Name, Role: role.Name,
			Message: fmt.Sprintf(format, args...),
		})
	}

	if p.Suspend {
		if st.Suspended {
			return false
		}
		st.Suspended = true
		event(events.KindScheduleSuspended, events.SeverityInfo, "suspended: no firing runs until suspend is cleared")
		return true
	}
	changed := false
	if st.Suspended {
		// Resuming recomputes from now: a suspended schedule catches
		// nothing up.
		st.Suspended = false
		st.SpecKey = ""
		event(events.KindScheduleSuspended, events.SeverityInfo, "resumed")
		changed = true
	}

	spec := p.Cron + "|" + p.Timezone
	if st.SpecKey != spec || st.NextDueAt.IsZero() {
		// First sight of this schedule, or a changed one: compute the next
		// due time from now and fire nothing in the past.
		next, err := p.NextFiring(now)
		if err != nil {
			log.Printf("schedule %s/%s/%s: %v", t.Workspace, t.Name, role.Name, err)
			return changed
		}
		st.SpecKey = spec
		c.setNextDue(st, p, now, next, event)
		return true
	}

	if now.Before(st.NextFireAt) {
		return changed
	}

	// Due. Walk past any due times the daemon slept through; the newest
	// is the one that may run.
	newest, missed := st.NextDueAt, 0
	for missed < maxCatchUpScan {
		n, err := p.NextFiring(newest)
		if err != nil || n.After(now) {
			break
		}
		newest = n
		missed++
	}
	fireAt := newest
	if missed == 0 {
		fireAt = st.NextFireAt
	}
	late := now.Sub(fireAt)
	next, err := p.NextFiring(newest)
	if err != nil {
		log.Printf("schedule %s/%s/%s: %v", t.Workspace, t.Name, role.Name, err)
		return changed
	}
	c.setNextDue(st, p, newest, next, event)

	deadline := p.StartingDeadline
	if deadline == 0 {
		deadline = scheduleGrace
	}
	if late > deadline {
		event(events.KindScheduleMissed, events.SeverityWarning,
			"missed %d firing(s); the newest, due %s, is %s late, past starting_deadline %s",
			missed+1, newest.Format(time.RFC3339), late.Round(time.Second), deadline)
		return true
	}
	id := api.FiringID(newest)
	switch {
	case st.Frozen:
		event(events.KindScheduleSkipped, events.SeverityWarning, "skipped firing %s: frozen by on_failure; marvel reset-health clears it", id)
		return true
	case withheld:
		event(events.KindScheduleSkipped, events.SeverityWarning, "skipped firing %s: the team's convergence posture is hold and nothing in it is live; marvel converge starts it", id)
		return true
	}
	replaced := false
	if len(live) > 0 {
		switch p.Concurrency {
		case api.ScheduleConcurrencyAllow:
		case api.ScheduleConcurrencyReplace:
			step.kill = live
			replaced = true
		default:
			event(events.KindScheduleSkipped, events.SeverityInfo, "skipped firing %s: overlap, %d run(s) of an earlier firing still live (concurrency forbid)", id, len(live))
			return true
		}
	}

	st.Firing = id
	st.FiringDueAt = newest
	st.FiringCatchUp = missed > 0 || late > scheduleGrace
	st.FiringMissed = missed
	st.Attempts = 0
	st.RetryAfter = time.Time{}
	st.Settled = false

	var detail []string
	if st.FiringCatchUp {
		detail = append(detail, "catch-up")
	}
	if missed > 0 {
		detail = append(detail, fmt.Sprintf("missed %d", missed))
	}
	msg := fmt.Sprintf("firing %s, due %s", id, newest.Format(time.RFC3339))
	if len(detail) > 0 {
		msg += " (" + strings.Join(detail, ", ") + ")"
	}
	if replaced {
		event(events.KindScheduleReplaced, events.SeverityInfo, "%s; killed %d run(s) of an earlier firing (concurrency replace)", msg, len(live))
	} else {
		event(events.KindScheduleFired, events.SeverityInfo, "%s", msg)
	}
	return true
}

// setNextDue stores the next due time and its jittered start, and reports
// the daylight-saving facts design 2a asks for at every next-due
// computation: an offset change between from and next, and a zone that
// observes daylight saving without dst_ack.
func (c *Controller) setNextDue(st *api.ScheduleStatus, p api.SchedulePolicy, from, next time.Time, event func(events.Kind, events.Severity, string, ...any)) {
	st.NextDueAt = next
	st.NextFireAt = next.Add(c.drawJitter(p.Jitter))
	shift, observes := api.ScheduleZoneFacts(p.Timezone, from, next)
	if shift != "" {
		event(events.KindScheduleDSTShift, events.SeverityInfo, "%s; the next firing is %s", shift, next.Format(time.RFC3339))
	}
	if observes && !p.DSTAck {
		event(events.KindScheduleDSTUnacknowledged, events.SeverityWarning,
			"timezone %s observes daylight saving and the schedule has no dst_ack; it keeps firing in local time. Add dst_ack = true or use a fixed offset", p.Timezone)
	}
}
