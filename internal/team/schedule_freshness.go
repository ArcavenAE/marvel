package team

import (
	"fmt"
	"log"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// reconcileScheduleFreshness is the freshness alarm (scheduled-runs
// section 7). For every scheduled role it emits schedule.stale when the
// role goes stale and schedule.fresh when it recovers, once per
// transition. The
// stale state lives in the store, so a daemon restart neither forgets a
// stale schedule nor announces it twice. It also drops the status of any
// role that no longer carries a schedule, so a later schedule under the
// same name starts fresh.
//
// Diagnostic only: it changes no plan and spawns nothing.
func (c *Controller) reconcileScheduleFreshness(teams []api.Team) {
	now := c.nowUTC()
	scheduled := make(map[string]bool)
	for i := range teams {
		t := &teams[i]
		for j := range t.Roles {
			role := &t.Roles[j]
			if role.Schedule == nil {
				continue
			}
			key := t.Workspace + "/" + t.Name + "/" + role.Name
			scheduled[key] = true
			c.evaluateScheduleFreshness(t, role, key, now)
		}
	}
	for _, st := range c.store.ListScheduleStatus() {
		if scheduled[st.Key] {
			continue
		}
		if err := c.store.DeleteScheduleStatus(st.Key); err != nil {
			log.Printf("warning: drop schedule status %s: %v", st.Key, err)
		}
	}
}

func (c *Controller) evaluateScheduleFreshness(t *api.Team, role *api.Role, key string, now time.Time) {
	staleAfter := role.Schedule.StaleAfter
	var changed bool
	st, err := c.store.UpdateScheduleStatus(key, func(st *api.ScheduleStatus) bool {
		// The clock may have created the record this tick; the first
		// evaluation's since stamp must be kept even with no transition.
		stamped := st.Since.IsZero()
		changed = st.EvaluateFreshness(now, staleAfter)
		return changed || stamped
	})
	if err != nil {
		log.Printf("warning: schedule freshness %s: %v", key, err)
		return
	}
	if !changed {
		return
	}
	ev := events.Event{
		Kind:      events.KindScheduleStale,
		Severity:  events.SeverityWarning,
		Workspace: t.Workspace,
		Team:      t.Name,
		Role:      role.Name,
	}
	if st.Stale {
		last := "no run has succeeded since " + st.Since.Format(time.RFC3339)
		if !st.LastSucceededAt.IsZero() {
			last = "last succeeded " + st.LastSucceededAt.Format(time.RFC3339)
		}
		ev.Message = fmt.Sprintf("stale: no succeeded run within stale_after %s; %s", staleAfter, last)
	} else {
		ev.Kind = events.KindScheduleFresh
		ev.Severity = events.SeverityInfo
		ev.Message = fmt.Sprintf("recovered: a run succeeded at %s, within stale_after %s", st.LastSucceededAt.Format(time.RFC3339), staleAfter)
		// With no success, only a raised stale_after can end the stale
		// state; there is no run to name.
		if st.LastSucceededAt.IsZero() {
			ev.Message = fmt.Sprintf("recovered: stale_after raised to %s", staleAfter)
		}
	}
	events.Emit(c.Events, ev)
}
