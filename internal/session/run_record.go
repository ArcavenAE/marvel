package session

import (
	"fmt"
	"log"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	rtevents "github.com/arcavenae/marvel/internal/runtime/events"
	"github.com/arcavenae/marvel/internal/usage"
)

// runTail is what a run record takes from a session's stream: the final
// assistant message, the denial count from the final result line, and the
// accountant's spend read just before the session's accounting retires.
//
// The result is the last assistant message rather than a field on
// session.ended, so the adapter event contract (contracts/schema) and
// everything that consumes it are unchanged. On claude the two carry the
// same text.
type runTail struct {
	result  string
	denials int
	spend   usage.Spend
	metered bool
}

// spendReader is the accountant's per-session spend. Optional on the
// UsageObserver so a recorder in tests need not implement it.
type spendReader interface {
	SessionSpend(agentID string) (usage.Spend, bool)
}

// note keeps the parts of ev a run record needs. It runs on every event,
// so it does no more than copy a string or a length.
func (d *usageDrain) note(ev rtevents.Event) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch data := ev.Data.(type) {
	case rtevents.MessageData:
		if data.Role == "assistant" && data.Text != "" {
			d.tail.result = data.Text
		}
	case rtevents.SessionEndedData:
		if data.Metering != nil {
			d.tail.denials = len(data.Metering.PermissionDenials)
		}
	}
}

// recordScheduledRun adds a session's run to its role's history when the
// role is on a schedule, settles it against the role's current firing
// (retries, on_failure), and emits run.succeeded, run.failed or
// run.cancelled, plus schedule.frozen when the run froze the schedule.
// The event carries status only, never the result text (scheduled-runs
// section 5). A session whose role is not scheduled records nothing.
func (m *Manager) recordScheduledRun(sess api.Session, outcome api.RunOutcome, exitStatus string, tail runTail) {
	team, err := m.store.GetTeam(sess.Workspace + "/" + sess.Team)
	if err != nil {
		return
	}
	var sched *api.SchedulePolicy
	for i := range team.Roles {
		if team.Roles[i].Name == sess.Role {
			sched = team.Roles[i].Schedule
			break
		}
	}
	if sched == nil {
		return
	}

	rec := api.RunRecord{
		Session:    sess.Key(),
		StartedAt:  sess.CreatedAt,
		EndedAt:    m.now(),
		Firing:     sess.Firing,
		Outcome:    outcome,
		ExitStatus: exitStatus,
		Tokens: api.RunTokens{
			Prompt:       tail.spend.PromptTokens,
			Out:          tail.spend.Out,
			CostUSD:      tail.spend.CostUSD,
			CostReported: tail.spend.CostReported,
			Metered:      tail.metered,
		},
		PermissionDenials: tail.denials,
	}
	rec.SetResult(tail.result)

	key := sess.Workspace + "/" + sess.Team + "/" + sess.Role
	bound := sched.HistoryBound()
	policy := *sched
	var froze bool
	if _, err := m.store.UpdateScheduleStatus(key, func(st *api.ScheduleStatus) bool {
		if rec.Firing != "" && rec.Firing == st.Firing {
			rec.DueAt = st.FiringDueAt
			rec.CatchUp = st.FiringCatchUp
		}
		st.AddRun(rec, bound)
		froze = st.SettleRun(rec, policy, rec.EndedAt)
		return true
	}); err != nil {
		log.Printf("warning: session %s: record run: %v", sess.Key(), err)
	}

	kind, sev := events.KindRunSucceeded, events.SeverityInfo
	switch outcome {
	case api.RunFailed:
		kind, sev = events.KindRunFailed, events.SeverityWarning
	case api.RunCancelled:
		kind = events.KindRunCancelled
	}
	tokens := "unmetered"
	if rec.Tokens.Metered {
		tokens = fmt.Sprintf("tokens prompt=%d out=%d", rec.Tokens.Prompt, rec.Tokens.Out)
	}
	msg := fmt.Sprintf("%s (exit %s) after %s; %s", outcome, orNone(exitStatus), rec.Duration().Round(time.Second), tokens)
	if outcome == api.RunCancelled {
		msg = fmt.Sprintf("cancelled by a kill after %s; %s", rec.Duration().Round(time.Second), tokens)
	}
	if rec.PermissionDenials > 0 {
		msg += fmt.Sprintf("; %d permission denials", rec.PermissionDenials)
	}
	if froze {
		events.Emit(m.Events, events.Event{
			Kind:      events.KindScheduleFrozen,
			Severity:  events.SeverityWarning,
			Workspace: sess.Workspace,
			Team:      sess.Team,
			Role:      sess.Role,
			Session:   sess.Key(),
			Message:   fmt.Sprintf("firing %s failed with its retries used up; on_failure = freeze stops the schedule until reset-health", rec.Firing),
		})
	}
	events.Emit(m.Events, events.Event{
		Kind:      kind,
		Severity:  sev,
		Workspace: sess.Workspace,
		Team:      sess.Team,
		Role:      sess.Role,
		Session:   sess.Key(),
		Message:   msg,
	})
}

func orNone(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
