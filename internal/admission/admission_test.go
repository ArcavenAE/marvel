package admission

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

var testSince = time.Date(2026, 8, 1, 14, 8, 0, 0, time.UTC)

// metered returns a snapshot whose token figures are real, so a test row
// that means "measured and under budget" cannot accidentally read as
// "never measured".
func metered(live, declared, tokens int) Snapshot {
	return Snapshot{
		Workspace:        "fanout",
		Team:             "crew",
		LiveSessions:     live,
		DeclaredSessions: declared,
		TokensObserved:   tokens,
		TokensMetered:    true,
		TokensSeen:       live,
		Since:            testSince,
	}
}

// TestCheck is the whole admission arithmetic (aae-orc-qiay), table-driven
// over the cases that decide the design: the count boundary, the repair
// exemption and its one hole, shift overlap by shape, and every partiality
// state of the token clause.
func TestCheck(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		budget       api.Budget
		snap         Snapshot
		req          Request
		want         Decision
		wantGranted  int
		wantDeciding api.Dimension
		wantClauses  int
	}{
		// A1. The default-open guarantee, as arithmetic.
		{
			name:        "undeclared budget admits and evaluates nothing",
			budget:      api.Budget{},
			snap:        metered(99, 99, 9_000_000),
			req:         Request{Role: "crew", Want: 40, Kind: Growth},
			want:        Admit,
			wantGranted: 40,
			wantClauses: 0,
		},
		// A2. The count boundary.
		{
			name:         "count at the ceiling admits",
			budget:       api.Budget{MaxSessions: 6},
			snap:         metered(3, 3, 0),
			req:          Request{Role: "crew", Want: 3, Kind: Growth},
			want:         Admit,
			wantGranted:  3,
			wantDeciding: api.DimMaxSessions,
			wantClauses:  1,
		},
		{
			name:         "one over the ceiling refuses",
			budget:       api.Budget{MaxSessions: 6},
			snap:         metered(3, 3, 0),
			req:          Request{Role: "crew", Want: 4, Kind: Growth},
			want:         Refuse,
			wantGranted:  0,
			wantDeciding: api.DimMaxSessions,
			wantClauses:  1,
		},
		{
			name:        "a request that adds nothing is never refused",
			budget:      api.Budget{MaxSessions: 6},
			snap:        metered(40, 40, 0),
			req:         Request{Role: "crew", Want: 0, Kind: Growth},
			want:        Admit,
			wantGranted: 0,
			wantClauses: 0,
		},
		// A3. Partial admission, the reconciler's side of the asymmetry.
		{
			name:         "AllowPartial grants headroom instead of nothing",
			budget:       api.Budget{MaxSessions: 6},
			snap:         metered(5, 5, 0),
			req:          Request{Role: "crew", Want: 3, Kind: Growth, AllowPartial: true},
			want:         Refuse,
			wantGranted:  1,
			wantDeciding: api.DimMaxSessions,
			wantClauses:  1,
		},
		// A4. Falsification: without shape-based overlap exemption, a budget
		// equal to declared replicas forbids every rolling shift.
		{
			name:         "shift overlap skips the count clause",
			budget:       api.Budget{MaxSessions: 3},
			snap:         metered(3, 3, 0),
			req:          Request{Want: 3, Kind: Growth, Overlap: true},
			want:         Admit,
			wantGranted:  3,
			wantDeciding: api.DimMaxSessions,
			wantClauses:  1,
		},
		// A5. Falsification: gating repair on live+want > limit means a
		// crashed replica never returns.
		{
			name:         "repair toward declared replicas is admitted",
			budget:       api.Budget{MaxSessions: 3},
			snap:         metered(2, 3, 0),
			req:          Request{Role: "crew", Want: 1, Kind: Repair, AllowPartial: true},
			want:         Admit,
			wantGranted:  1,
			wantDeciding: api.DimMaxSessions,
			wantClauses:  1,
		},
		// A6. The hole the R1 invariant leaves: a declaration that is itself
		// over budget is an operator edit, not a spawn.
		{
			name:         "repair refused when the declaration exceeds the ceiling",
			budget:       api.Budget{MaxSessions: 3},
			snap:         metered(2, 7, 0),
			req:          Request{Role: "crew", Want: 3, Kind: Repair, AllowPartial: true},
			want:         Refuse,
			wantGranted:  1,
			wantDeciding: api.DimMaxSessions,
			wantClauses:  1,
		},
		// A6b. The same over-declaration, asking for what fits. Nothing is
		// refused, so the verdict must not say refused: a Refuse here latches
		// a reconciler hold and emits a warning event for a tick that
		// satisfied the role in full.
		{
			name:         "an over-declaration this request fits inside admits",
			budget:       api.Budget{MaxSessions: 3},
			snap:         metered(2, 7, 0),
			req:          Request{Role: "crew", Want: 1, Kind: Repair, AllowPartial: true},
			want:         Admit,
			wantGranted:  1,
			wantDeciding: api.DimMaxSessions,
			wantClauses:  1,
		},
		// A6c. Falsification of the overspawn defect: unclamped, headroom
		// larger than the ask granted the whole headroom, so the reconciler
		// spawned past role.Replicas and deleted the excess on the next tick.
		{
			name:         "a partial grant never exceeds the request",
			budget:       api.Budget{MaxSessions: 5},
			snap:         metered(0, 6, 0),
			req:          Request{Role: "sup", Want: 1, Kind: Repair, AllowPartial: true},
			want:         Admit,
			wantGranted:  1,
			wantDeciding: api.DimMaxSessions,
			wantClauses:  1,
		},
		// A7. The token clause, including the >= boundary.
		{
			name:         "tokens under the ceiling admit",
			budget:       api.Budget{MaxTokens: 2_000_000},
			snap:         metered(2, 2, 412_118),
			req:          Request{Role: "crew", Want: 1, Kind: Growth},
			want:         Admit,
			wantGranted:  1,
			wantDeciding: api.DimMaxTokens,
			wantClauses:  1,
		},
		{
			name:         "tokens exactly at the ceiling refuse",
			budget:       api.Budget{MaxTokens: 2_000_000},
			snap:         metered(2, 2, 2_000_000),
			req:          Request{Role: "crew", Want: 1, Kind: Growth},
			want:         Refuse,
			wantGranted:  0,
			wantDeciding: api.DimMaxTokens,
			wantClauses:  1,
		},
		{
			name:         "tokens over the ceiling refuse",
			budget:       api.Budget{MaxTokens: 2_000_000},
			snap:         metered(2, 2, 2_118_443),
			req:          Request{Role: "crew", Want: 3, Kind: Growth},
			want:         Refuse,
			wantGranted:  0,
			wantDeciding: api.DimMaxTokens,
			wantClauses:  1,
		},
		// A8. The governance row. Partiality can only understate, so a
		// measured breach on a partial total is still a breach.
		{
			name:   "over budget on a partial total still refuses",
			budget: api.Budget{MaxTokens: 2_000_000},
			snap: func() Snapshot {
				s := metered(5, 5, 2_118_443)
				s.TokensPartial = true
				return s
			}(),
			req:          Request{Role: "crew", Want: 3, Kind: Growth},
			want:         Refuse,
			wantGranted:  0,
			wantDeciding: api.DimMaxTokens,
			wantClauses:  1,
		},
		// A9. Falsification: refusing here would be a refusal caused by
		// absent data, which is the failure internal/usage names.
		{
			name:   "under budget on a partial total admits",
			budget: api.Budget{MaxTokens: 2_000_000},
			snap: func() Snapshot {
				s := metered(5, 5, 412_118)
				s.TokensPartial = true
				return s
			}(),
			req:          Request{Role: "crew", Want: 3, Kind: Growth},
			want:         Admit,
			wantGranted:  3,
			wantDeciding: api.DimMaxTokens,
			wantClauses:  1,
		},
		// A10. Nothing measured yet is not the same as spent nothing.
		{
			name:         "no usage observed yet is indeterminate",
			budget:       api.Budget{MaxTokens: 2_000_000},
			snap:         Snapshot{Workspace: "fanout", Team: "crew", LiveSessions: 3, DeclaredSessions: 3, TokensMetered: true},
			req:          Request{Role: "crew", Want: 3, Kind: Growth},
			want:         Indeterminate,
			wantGranted:  3,
			wantDeciding: api.DimMaxTokens,
			wantClauses:  1,
		},
		// A11. No meter wired at all.
		{
			name:         "no meter is indeterminate, never zero spend",
			budget:       api.Budget{MaxTokens: 2_000_000},
			snap:         Snapshot{Workspace: "fanout", Team: "crew", LiveSessions: 3, DeclaredSessions: 3},
			req:          Request{Role: "crew", Want: 3, Kind: Growth},
			want:         Indeterminate,
			wantGranted:  3,
			wantDeciding: api.DimMaxTokens,
			wantClauses:  1,
		},
		// A12. on_unmeasured is the operator's fail-closed instrument.
		{
			name:         "on_unmeasured refuse fails closed",
			budget:       api.Budget{MaxTokens: 2_000_000, OnUnmeasured: api.UnmeasuredRefuse},
			snap:         Snapshot{Workspace: "fanout", Team: "crew", LiveSessions: 3, DeclaredSessions: 3, TokensMetered: true},
			req:          Request{Role: "crew", Want: 3, Kind: Growth},
			want:         Refuse,
			wantGranted:  0,
			wantDeciding: api.DimMaxTokens,
			wantClauses:  1,
		},
		// A13. Overlap is a count-shaped exemption only: a new generation is
		// a new spender.
		{
			name:         "shift overlap does not skip a cumulative clause",
			budget:       api.Budget{MaxSessions: 3, MaxTokens: 2_000_000},
			snap:         metered(3, 3, 2_118_443),
			req:          Request{Want: 3, Kind: Growth, Overlap: true},
			want:         Refuse,
			wantGranted:  0,
			wantDeciding: api.DimMaxTokens,
			wantClauses:  2,
		},
		// A14. Precedence: a measured breach outranks an unevaluable clause.
		{
			name:         "count exceeded outranks tokens unmeasured",
			budget:       api.Budget{MaxSessions: 6, MaxTokens: 2_000_000},
			snap:         Snapshot{Workspace: "fanout", Team: "crew", LiveSessions: 6, DeclaredSessions: 6, TokensMetered: true},
			req:          Request{Role: "crew", Want: 1, Kind: Growth},
			want:         Refuse,
			wantGranted:  0,
			wantDeciding: api.DimMaxSessions,
			wantClauses:  2,
		},
		// R2, structurally: repair never evaluates a monotonic clause, so an
		// exhausted token budget cannot make a team unrepairable.
		{
			name:         "repair never evaluates the token clause",
			budget:       api.Budget{MaxSessions: 3, MaxTokens: 2_000_000},
			snap:         metered(2, 3, 9_000_000),
			req:          Request{Role: "crew", Want: 1, Kind: Repair, AllowPartial: true},
			want:         Admit,
			wantGranted:  1,
			wantDeciding: api.DimMaxSessions,
			wantClauses:  1,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := Check(tt.budget, tt.snap, tt.req)
			if v.Decision != tt.want {
				t.Errorf("Decision = %q, want %q", v.Decision, tt.want)
			}
			if v.Granted != tt.wantGranted {
				t.Errorf("Granted = %d, want %d", v.Granted, tt.wantGranted)
			}
			if v.Deciding != tt.wantDeciding {
				t.Errorf("Deciding = %q, want %q", v.Deciding, tt.wantDeciding)
			}
			if len(v.Clauses) != tt.wantClauses {
				t.Errorf("evaluated %d clause(s), want %d: %+v", len(v.Clauses), tt.wantClauses, v.Clauses)
			}
			if v.Refused() != (tt.want == Refuse) {
				t.Errorf("Refused() = %v, want %v", v.Refused(), tt.want == Refuse)
			}
		})
	}
}

// TestUnderBudgetPartialCarriesANotice covers A9's other half: admitting on
// an incomplete total is only honest if the incompleteness is said out loud.
func TestUnderBudgetPartialCarriesANotice(t *testing.T) {
	t.Parallel()
	s := metered(5, 5, 412_118)
	s.TokensPartial = true
	v := Check(api.Budget{MaxTokens: 2_000_000}, s, Request{Role: "crew", Want: 1, Kind: Growth})
	if len(v.Clauses) != 1 {
		t.Fatalf("expected one clause, got %+v", v.Clauses)
	}
	if !strings.Contains(v.Clauses[0].Note, "partial") {
		t.Errorf("clause note = %q, want it to name the partial total", v.Clauses[0].Note)
	}
}

// TestHeadroomCoveringTheAskIsNotARefusal is the contract the never-silent
// requirement depends on in reverse: the refusal surface must not report
// refusals that did not happen, or a hit on `marvel events --kind
// admission.refused` cannot be trusted.
//
// The defect was structural rather than arithmetic. check set Decision=Refuse
// on an exceeded clause, then overwrote Granted for the partial case without
// revisiting the decision, and Refused() reads the decision alone. A
// reconcile tick that spawned every requested session still logged, emitted a
// warning event, and latched Team.Admission{Held:true}.
func TestHeadroomCoveringTheAskIsNotARefusal(t *testing.T) {
	t.Parallel()
	// The over-declaration is the only way to reach an exceeded count clause
	// on the repair path (R1), so it is also the only way to reach the bug.
	v := CheckSessions(api.Budget{MaxSessions: 3}, 0, 4, Request{
		Role: "a", Want: 3, Kind: Repair, AllowPartial: true,
	})
	if v.Decision != Admit {
		t.Errorf("Decision = %q, want admit: every requested spawn fits", v.Decision)
	}
	if v.Refused() {
		t.Errorf("Refused() = true with Granted %d of Want %d", v.Granted, v.Want)
	}
	if v.Granted != 3 {
		t.Errorf("Granted = %d, want 3", v.Granted)
	}
	// The over-ceiling declaration is still visible: the fix suppresses the
	// false refusal, not the evidence.
	if len(v.Clauses) != 1 || v.Clauses[0].State != Exceeded {
		t.Fatalf("clauses = %+v, want one exceeded count clause", v.Clauses)
	}
	if v.Deciding != api.DimMaxSessions {
		t.Errorf("Deciding = %q, want max_sessions", v.Deciding)
	}
	// Granted is clamped to Want, so no caller can spawn past what it asked
	// for and no rendered count can go negative.
	over := CheckSessions(api.Budget{MaxSessions: 5}, 0, 6, Request{
		Role: "sup", Want: 1, Kind: Repair, AllowPartial: true,
	})
	if over.Granted != 1 {
		t.Errorf("Granted = %d with headroom 5 and Want 1, want 1", over.Granted)
	}
	// Unclamped, this rendered "refused -4 of 1 spawn(s) ... granted 5".
	if reason := over.Reason(TriggerReconcile); strings.Contains(reason, "refused") {
		t.Errorf("Reason = %q, want no refusal claim on an admitted verdict", reason)
	}
}

// TestSuspectTotalIsNotedNotDecided covers the cumulation-violation case:
// the meter's own distrust of a total changes what the operator is told, not
// whether the spawn is refused.
func TestSuspectTotalIsNotedNotDecided(t *testing.T) {
	t.Parallel()
	s := metered(2, 2, 412_118)
	s.TokensSuspect = true
	v := Check(api.Budget{MaxTokens: 2_000_000}, s, Request{Role: "crew", Want: 1, Kind: Growth})
	if v.Decision != Admit {
		t.Fatalf("Decision = %q, want admit", v.Decision)
	}
	if !strings.Contains(v.Clauses[0].Note, "suspect") {
		t.Errorf("clause note = %q, want it to name the suspect total", v.Clauses[0].Note)
	}
}

// TestReasonIsOneLineWithTheArithmetic covers A15. events.Event carries no
// structured payload, so the Message is the machine-readable surface: it
// must stay one line and it must carry the numbers that decided.
func TestReasonIsOneLineWithTheArithmetic(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		budget   api.Budget
		snap     Snapshot
		req      Request
		trigger  Trigger
		contains []string
	}{
		{
			name:     "count refusal names both numbers and the unit",
			budget:   api.Budget{MaxSessions: 6},
			snap:     metered(6, 6, 0),
			req:      Request{Role: "crew", Want: 34, Kind: Growth},
			trigger:  TriggerScale,
			contains: []string{"refused 34 of 34", "role crew", "6 live", "34 requested", "sessions", "max_sessions=6", "trigger=scale"},
		},
		{
			name:     "partial grant names what was granted",
			budget:   api.Budget{MaxSessions: 6},
			snap:     metered(4, 4, 0),
			req:      Request{Role: "crew", Want: 5, Kind: Growth, AllowPartial: true},
			trigger:  TriggerReconcile,
			contains: []string{"refused 3 of 5", "granted 2", "trigger=reconcile"},
		},
		{
			name:     "token refusal names the window",
			budget:   api.Budget{MaxTokens: 2_000_000},
			snap:     metered(2, 2, 2_118_443),
			req:      Request{Role: "crew", Want: 3, Kind: Growth},
			trigger:  TriggerApply,
			contains: []string{"2118443", "tokens", "max_tokens=2000000", "since=", "trigger=apply"},
		},
		{
			name:     "repair refusal names the disagreeing declaration",
			budget:   api.Budget{MaxSessions: 3},
			snap:     metered(2, 7, 0),
			req:      Request{Role: "crew", Want: 1, Kind: Repair},
			trigger:  TriggerReconcile,
			contains: []string{"max_sessions=3", "declares 7 sessions"},
		},
		{
			name:     "unmeasured admission names the clause and the mode",
			budget:   api.Budget{MaxTokens: 2_000_000},
			snap:     Snapshot{TokensMetered: true},
			req:      Request{Role: "crew", Want: 3, Kind: Growth},
			trigger:  TriggerApply,
			contains: []string{"max_tokens declared", "no token usage observed yet", "on_unmeasured=admit", "trigger=apply"},
		},
		{
			name:     "unmeasured refusal names the fail-closed mode",
			budget:   api.Budget{MaxTokens: 2_000_000, OnUnmeasured: api.UnmeasuredRefuse},
			snap:     Snapshot{TokensMetered: true},
			req:      Request{Role: "crew", Want: 3, Kind: Growth},
			trigger:  TriggerRun,
			contains: []string{"refused 3 of 3", "on_unmeasured=refuse", "trigger=run"},
		},
		{
			name:     "admitted verdict still reads as a sentence",
			budget:   api.Budget{MaxSessions: 6},
			snap:     metered(2, 2, 0),
			req:      Request{Role: "crew", Want: 1, Kind: Growth},
			trigger:  TriggerRun,
			contains: []string{"within budget", "role crew", "trigger=run"},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Check(tt.budget, tt.snap, tt.req).Reason(tt.trigger)
			if strings.ContainsAny(got, "\n\r") {
				t.Errorf("Reason spans more than one line: %q", got)
			}
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("Reason = %q, missing %q", got, want)
				}
			}
		})
	}
}

// TestKeyLatchesOnTheDecisionNotTheMessage covers A16. The reconciler emits
// only when the key changes, so a key that moved with the message text would
// re-fire on any formatting change and flush the event ring; a key that did
// not move on a change of deciding dimension would hide new information.
func TestKeyLatchesOnTheDecisionNotTheMessage(t *testing.T) {
	t.Parallel()
	budget := api.Budget{MaxSessions: 6, MaxTokens: 2_000_000}
	req := Request{Role: "crew", Want: 4, Kind: Growth}

	overCount := Check(budget, metered(6, 6, 0), req)
	overCountAgain := Check(budget, metered(6, 6, 0), req)
	if overCount.Key() != overCountAgain.Key() {
		t.Errorf("identical verdicts produced different keys: %q vs %q", overCount.Key(), overCountAgain.Key())
	}

	// Same decision, same deciding dimension, different arithmetic in the
	// message: the ring must not see a second event.
	overCountBigger := Check(budget, metered(9, 9, 0), req)
	if overCountBigger.Key() != overCount.Key() {
		t.Errorf("a changed message moved the key: %q vs %q", overCountBigger.Key(), overCount.Key())
	}
	if overCountBigger.Reason(TriggerReconcile) == overCount.Reason(TriggerReconcile) {
		t.Fatal("test is vacuous: the two messages are identical")
	}

	// A different dimension deciding IS new information.
	overTokens := Check(budget, metered(2, 2, 2_118_443), req)
	if overTokens.Key() == overCount.Key() {
		t.Errorf("count and token refusals share a key: %q", overTokens.Key())
	}

	// So is stopping refusing.
	admitted := Check(budget, metered(1, 1, 10), req)
	if admitted.Key() == overCount.Key() {
		t.Errorf("admit and refuse share a key: %q", admitted.Key())
	}
}

// TestCheckSessionsSkipsCumulativeClauses covers A17: the reconciler's
// entry point is Check with every cumulative clause dropped, which is what
// keeps a monotonic meter off the repair path (R2).
func TestCheckSessionsSkipsCumulativeClauses(t *testing.T) {
	t.Parallel()
	both := api.Budget{MaxSessions: 6, MaxTokens: 2_000_000}
	countOnly := api.Budget{MaxSessions: 6}
	req := Request{Role: "crew", Want: 2, Kind: Growth, AllowPartial: true}

	for _, live := range []int{0, 3, 5, 6, 9} {
		got := CheckSessions(both, live, live, req)
		want := Check(countOnly, Snapshot{LiveSessions: live, DeclaredSessions: live}, req)
		if got.Decision != want.Decision || got.Granted != want.Granted || got.Deciding != want.Deciding {
			t.Errorf("live=%d: CheckSessions = (%s, %d, %s), want (%s, %d, %s)",
				live, got.Decision, got.Granted, got.Deciding, want.Decision, want.Granted, want.Deciding)
		}
		if len(got.Clauses) != 1 {
			t.Errorf("live=%d: evaluated %d clause(s), want the count clause alone", live, len(got.Clauses))
		}
	}
}

// TestRowsReportEveryDeclaredDimension covers the `marvel get budgets`
// assembly: a team with no budget contributes nothing, and an unmeasured
// token figure is reported as unmetered rather than as headroom the operator
// does not have.
func TestRowsReportEveryDeclaredDimension(t *testing.T) {
	t.Parallel()
	if rows := Rows(api.Team{Workspace: "ws", Name: "plain"}, metered(3, 3, 0)); rows != nil {
		t.Errorf("undeclared budget produced %d row(s), want none", len(rows))
	}

	team := api.Team{Workspace: "fanout", Name: "crew", Budget: api.Budget{MaxSessions: 6, MaxTokens: 2_000_000}}
	rows := Rows(team, metered(6, 6, 412_118))
	if len(rows) != 2 {
		t.Fatalf("got %d row(s), want 2: %+v", len(rows), rows)
	}
	// A healthy team whose declared replicas equal its ceiling refuses
	// nothing, so this row must not read as a refusal. See RowAtCeiling.
	if rows[0].Dimension != api.DimMaxSessions || rows[0].State != RowAtCeiling || rows[0].Headroom != 0 {
		t.Errorf("session row = %+v, want max_sessions at-ceiling with no headroom", rows[0])
	}
	if rows[1].Dimension != api.DimMaxTokens || rows[1].State != RowOK || rows[1].Headroom != 1_587_882 {
		t.Errorf("token row = %+v, want max_tokens ok with 1587882 headroom", rows[1])
	}
	if rows[1].Window != testSince {
		t.Errorf("token row window = %s, want %s", rows[1].Window, testSince)
	}

	unmetered := Rows(team, Snapshot{LiveSessions: 2, DeclaredSessions: 6})
	if unmetered[1].State != RowUnmetered {
		t.Errorf("token row state = %q, want %q when nothing is measured", unmetered[1].State, RowUnmetered)
	}
}

// TestRowsSeparateAtCeilingFromRefusing covers the primary "which dimension
// tripped" surface. Keying refusal on zero headroom made refusing the resting
// state of every team sized at its ceiling, so a real refusal and the intended
// configuration rendered identically.
func TestRowsSeparateAtCeilingFromRefusing(t *testing.T) {
	t.Parallel()
	budget := api.Budget{MaxSessions: 2}

	held := api.Team{
		Workspace: "fanout", Name: "crew", Budget: budget,
		Admission: api.AdmissionState{
			Held: true, Role: "b",
			Reason: "refused 1 of 1 spawn(s) for role b: 2 live sessions against max_sessions=2 (trigger=reconcile)",
		},
	}
	rows := Rows(held, Snapshot{LiveSessions: 2, DeclaredSessions: 3})
	if rows[0].State != RowRefusing {
		t.Errorf("state = %q with a standing hold, want %q", rows[0].State, RowRefusing)
	}
	if !strings.Contains(rows[0].Note, "role b") {
		t.Errorf("note = %q, want the held role's arithmetic", rows[0].Note)
	}

	// A shift reads above the ceiling by design (R5). Not a refusal, and the
	// note has to name the mechanism or the operator is left with two numbers
	// that cannot both be right.
	shifting := api.Team{
		Workspace: "fanout", Name: "crew", Budget: budget,
		Shift: api.ShiftState{Phase: api.ShiftDraining},
	}
	rows = Rows(shifting, Snapshot{LiveSessions: 4, DeclaredSessions: 2})
	if rows[0].State == RowRefusing {
		t.Errorf("state = %q during a shift, want anything but %q", rows[0].State, RowRefusing)
	}
	if rows[0].Observed != 4 || rows[0].Headroom != 0 {
		t.Errorf("row = %+v, want the overshoot reported verbatim", rows[0])
	}
	if !strings.Contains(rows[0].Note, "shift") {
		t.Errorf("note = %q, want it to name the shift overlap", rows[0].Note)
	}

	// Headroom left, no hold, no shift: plain ok.
	plain := api.Team{Workspace: "fanout", Name: "crew", Budget: budget}
	rows = Rows(plain, Snapshot{LiveSessions: 1, DeclaredSessions: 2})
	if rows[0].State != RowOK || rows[0].Note != "" {
		t.Errorf("row = %+v, want %q with no note", rows[0], RowOK)
	}
}
