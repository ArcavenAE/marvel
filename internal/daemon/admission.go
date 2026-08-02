package daemon

import (
	"fmt"
	"log"

	"github.com/arcavenae/marvel/internal/admission"
	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// AdmissionSnapshot builds the measured state one admission check
// evaluates for a team: exact store counts plus one TeamSpend call,
// resolving the meter's absence states into flags.
//
// A nil accountant yields TokensMetered false rather than a zero total,
// because "no meter" and "spent nothing" must not read alike — a gate that
// reads an unresolved measurement as zero admits everything (see
// internal/usage/reader.go). Satisfies team.Snapshotter.
func (d *Daemon) AdmissionSnapshot(t api.Team) admission.Snapshot {
	s := admission.Snapshot{
		Workspace:    t.Workspace,
		Team:         t.Name,
		LiveSessions: api.CountAlive(d.store.ListSessionsByTeam(t.Workspace, t.Name)),
	}
	for i := range t.Roles {
		s.DeclaredSessions += t.Roles[i].Replicas
	}
	if d.usage == nil {
		return s
	}
	tt := d.usage.TeamSpend(t.Workspace, t.Name)
	s.TokensMetered = true
	// PromptTokens is the layout-normalized prompt figure; summing the raw
	// class fields would double count a subsumptive feed. Output and
	// reasoning tokens carry no layout.
	s.TokensObserved = tt.PromptTokens + tt.Out + tt.ReasoningOut
	s.TokensSeen = tt.LiveSessions + tt.EndedSessions
	s.Since = tt.Since
	// TeamTotals.Partial does not cover a pane adopted from a prior daemon.
	// Bind runs only inside attachInstance, which only Manager.Create calls,
	// so an adopted session has no accountant state at all: it contributes
	// nothing to the total AND does not set Partial. Comparing the store's
	// live count against the meter's catches it.
	unobserved := s.LiveSessions - tt.LiveSessions
	s.TokensPartial = tt.Partial || unobserved > 0
	s.TokensSuspect = d.usage.Stats().CumulationViolations > 0
	return s
}

// admitGrowth is the synchronous gate the operator's verbs consult. It
// returns nil when the action is admitted, or a populated error Response
// when refused.
//
// Whole-or-nothing on purpose: a declaration is operator intent, and
// marvel silently applying 6 replicas when the operator asked for 40 would
// be marvel editing intent, which is worse than refusing. The reconciler
// takes the opposite side (partial grants), because convergence is repair
// and some is better than none.
func (d *Daemon) admitGrowth(t api.Team, role string, want int, trig admission.Trigger) *Response {
	if !t.Budget.Declared() || want <= 0 {
		return nil
	}
	v := admission.Check(t.Budget, d.AdmissionSnapshot(t), admission.Request{
		Role: role,
		Want: want,
		Kind: admission.Growth,
	})
	reason := v.Reason(trig)
	switch v.Decision {
	case admission.Refuse:
		log.Printf("admission: %s refused: %s", t.Key(), reason)
		events.Emit(d.events, events.Event{
			Kind:      events.KindAdmissionRefused,
			Severity:  events.SeverityWarning,
			Workspace: t.Workspace,
			Team:      t.Name,
			Role:      role,
			Message:   reason,
		})
		return &Response{Error: fmt.Sprintf(
			"%s: %s. Nothing changed; raise the budget in the manifest or free headroom first",
			t.Key(), reason,
		)}
	case admission.Indeterminate:
		log.Printf("admission: %s admitted unmeasured: %s", t.Key(), reason)
		events.Emit(d.events, events.Event{
			Kind:      events.KindAdmissionUnmeasured,
			Severity:  events.SeverityWarning,
			Workspace: t.Workspace,
			Team:      t.Name,
			Role:      role,
			Message:   reason,
		})
	}
	return nil
}

// admitDeclaration enforces the declaration clause at the one verb that can
// raise a replica count without passing through the manifest parser.
//
// api.validateManifestBudget gives handleApply the invariant everything else
// rests on: sum(role.Replicas) <= max_sessions, which is why converging a
// role toward its declared replicas is provably safe (admission R1). Scale
// had only the spawn gate, which compares LIVE sessions against the ceiling.
// Whenever live < declared, and that is the normal state during a crash loop
// (which is also when an operator reaches for scale), a scale-up was
// admitted while committing a declaration the parser refuses.
//
// The result is exactly what handleApply's gate exists to prevent: a
// permanently unsatisfiable desired state. The last headroom slot goes to
// whichever role sorts earlier in manifest order, the deficient role is held
// forever, and nothing retries, because a refusal never bumps RestartCount
// and so no backoff ever re-fires.
//
// Whole-or-nothing and never a partial edit: a replica count is operator
// intent, and marvel committing a number the operator did not ask for would
// be marvel editing intent.
func (d *Daemon) admitDeclaration(t api.Team, role string, replicas, old int) *Response {
	if t.Budget.MaxSessions <= 0 || replicas <= old {
		return nil
	}
	declared := replicas - old
	for i := range t.Roles {
		declared += t.Roles[i].Replicas
	}
	if declared <= t.Budget.MaxSessions {
		return nil
	}
	reason := fmt.Sprintf(
		"refused role %s at %d replica(s): the team would declare %d sessions across %d role(s) against max_sessions=%d (trigger=%s)",
		role, replicas, declared, len(t.Roles), t.Budget.MaxSessions, admission.TriggerScale,
	)
	log.Printf("admission: %s refused: %s", t.Key(), reason)
	events.Emit(d.events, events.Event{
		Kind:      events.KindAdmissionRefused,
		Severity:  events.SeverityWarning,
		Workspace: t.Workspace,
		Team:      t.Name,
		Role:      role,
		Message:   reason,
	})
	return &Response{Error: fmt.Sprintf(
		"%s: %s. Nothing changed; raise the budget in the manifest or scale another role down first",
		t.Key(), reason,
	)}
}

// budgetRows assembles `marvel get budgets`: one row per declared
// dimension per team, none for a team that declares no budget.
//
// This is the only surface in marvel that answers "which dimension tripped
// and by how much". Nothing else exposes a spend or occupancy aggregate,
// and `marvel get teams` is deliberately left alone: a budget column would
// change output for every operator for a feature most teams do not declare.
func (d *Daemon) budgetRows() []admission.Row {
	var out []admission.Row
	for _, t := range d.store.ListTeams() {
		if !t.Budget.Declared() {
			continue
		}
		out = append(out, admission.Rows(t, d.AdmissionSnapshot(t))...)
	}
	return out
}

// logTokenBudgetWindows says out loud, once at daemon start, that a
// cumulative token budget counts from now.
//
// The accountant has no bolt bucket, so TeamSpend resets on a daemon
// restart and on `marvel daemon reexec` (which keeps agents alive but not
// accountant state). Persisting stream-derived readings is a separate
// decision this slice does not make, so the limit is stated rather than
// hidden: a silent reset would be the same class of defect as a guessed
// denominator.
func (d *Daemon) logTokenBudgetWindows() {
	for _, t := range d.store.ListTeams() {
		if t.Budget.MaxTokens <= 0 {
			continue
		}
		log.Printf("admission: %s declares budget.max_tokens=%d, counted since accounting started now (the meter is in-memory, so this window restarts with the daemon)",
			t.Key(), t.Budget.MaxTokens)
	}
}
