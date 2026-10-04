package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// evaluateLimits sets and clears the reading-sourced "limited" condition on
// every live session from its account's reading, once per transition. It is a
// restart-neutral advisory: nothing it does restarts, kills or shifts anything.
func (d *Daemon) evaluateLimits(now time.Time) {
	for _, sess := range d.store.ListSessions() {
		if !sess.State.CountsAsAlive() && sess.Limit == nil {
			continue
		}
		key := api.AccountKeyOf(sess)
		reading, state := d.accounts.Reading(key, now)
		var (
			change api.LimitChange
			reason string
			prov   *api.LimitProvenance
		)
		err := d.store.UpdateSession(sess.Key(), func(live *api.Session) error {
			next, ch, why := api.EvaluateLimit(live.Limit, key, reading, state, now)
			if ch == api.LimitUnchanged {
				return nil
			}
			change, reason = ch, why
			prov = next
			switch ch {
			case api.LimitSet, api.LimitRebound:
				live.Condition, live.Limit = api.ConditionLimited, next
			case api.LimitCleared:
				prov = live.Limit
				live.Condition, live.Limit = "", nil
			}
			return nil
		})
		// A rebind is a change of which window binds a session that stays
		// limited: stored, but not a transition, so no event.
		if err != nil || change == api.LimitUnchanged || change == api.LimitRebound {
			continue
		}
		ev := events.Event{
			Kind: events.KindSessionLimited, Severity: events.SeverityWarning,
			Workspace: sess.Workspace, Team: sess.Team, Role: sess.Role, Session: sess.Key(),
			Message: prov.Text(),
		}
		if change == api.LimitCleared {
			ev.Kind, ev.Severity = events.KindSessionUnlimited, events.SeverityInfo
			ev.Message = fmt.Sprintf("limit cleared (%s): was %s", reason, prov.Text())
		}
		events.Emit(d.events, ev)
		if change == api.LimitCleared && sess.State.CountsAsAlive() {
			// Option A (design 9.4): say the seat can be resumed. Nothing is typed.
			events.Emit(d.events, events.Event{
				Kind: events.KindSeatResumeProposed, Severity: events.SeverityWarning,
				Workspace: sess.Workspace, Team: sess.Team, Role: sess.Role, Session: sess.Key(),
				Message: fmt.Sprintf("%s is no longer limited (%s); it can be resumed", sess.Key(), reason),
			})
		}
	}
}

// RunLimits evaluates the limited condition on a tick, so a condition ends at
// its reset time without a new reading arriving. A sibling of the reconcile
// loop and not a step in it, as RunMetrics is.
func (d *Daemon) RunLimits(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().UTC()
			d.evaluateLimits(now)
			d.evaluatePaneMenus(now)
		}
	}
}

// stampLimitReading fills the view-only reading fields on session copies.
func (d *Daemon) stampLimitReading(sessions []api.Session, now time.Time) {
	for i := range sessions {
		reading, state := d.accounts.Reading(api.AccountKeyOf(sessions[i]), now)
		sessions[i].LimitReading = state
		sessions[i].LimitReadingText = api.LimitReadingText(reading, state)
	}
}
