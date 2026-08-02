package admission

import (
	"fmt"
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// Snapshot is the measured state one Check evaluates. The caller gathers
// it, so the arithmetic reads no store and no meter.
//
// Absence is carried by flags, never by a zero value. internal/usage warns
// in as many words that a gate reading an unresolved measurement as zero
// admits everything, so TokensObserved is meaningless unless TokensMetered
// is true.
type Snapshot struct {
	Workspace string
	Team      string

	// LiveSessions counts sessions whose State.CountsAsAlive(), across the
	// whole team.
	LiveSessions int
	// DeclaredSessions is the sum of Replicas across the team's roles.
	DeclaredSessions int

	// TokensObserved is the team's layout-normalized prompt + output +
	// reasoning tokens. Valid only when TokensMetered.
	TokensObserved int
	// TokensMetered is false when no Reader was available at all.
	TokensMetered bool
	// TokensPartial marks an incomplete total: some contributing session
	// was never observed, so the figure is a floor.
	TokensPartial bool
	// TokensSeen is how many sessions (live plus ended) the meter knows
	// about for this team. Zero with a zero Since means nothing has been
	// measured yet, which is distinct from a team that spent nothing.
	TokensSeen int
	// TokensSuspect marks a total the meter itself distrusts (a cumulative
	// feed read as levels inflates spend). Surfaced as a note rather than
	// changing the decision.
	TokensSuspect bool
	// Since is when accounting for this team began. Zero means never.
	Since time.Time
}

// Request is the spawn under consideration.
type Request struct {
	Role string
	// Want is how many sessions this action would add. A request that adds
	// nothing is never refused, which is what keeps a scale-down and a
	// no-op apply out of the gate. Mid-flight revocation (enforcement locus
	// 3) will ask "are we over right now" through this same arithmetic with
	// that guard lifted.
	Want int
	Kind Kind
	// Overlap marks sessions that are transient duplicates of sessions
	// already counted, as a shift's new generation is beside the old.
	// ShapeCount clauses skip an overlap; ShapeCumulative clauses do not.
	Overlap bool
	// AllowPartial lets the caller take headroom instead of nothing. The
	// reconciler sets it, because convergence is best-effort and 2 of 5 is
	// better for the operator than 0 of 5. The synchronous verbs do not: a
	// declaration is operator intent, and marvel silently applying 6 when
	// the operator asked for 40 would be marvel editing intent.
	AllowPartial bool
}

// Kind separates raising what a team declares from converging on what it
// already declared.
type Kind string

const (
	// Growth raises what the team declares: apply, scale up, ad-hoc run,
	// shift.
	Growth Kind = "growth"
	// Repair converges live sessions toward an already-admitted declared
	// count. Count clauses admit repair under the R1 invariant; cumulative
	// clauses are never evaluated on this path (R2).
	Repair Kind = "repair"
)

// Trigger names the operator action or loop that asked, for the event and
// the error text.
type Trigger string

const (
	TriggerApply     Trigger = "apply"
	TriggerScale     Trigger = "scale"
	TriggerRun       Trigger = "run"
	TriggerShift     Trigger = "shift"
	TriggerReconcile Trigger = "reconcile"
)

// Decision is the verdict's outcome.
type Decision string

const (
	Admit  Decision = "admit"
	Refuse Decision = "refuse"
	// Indeterminate means a declared clause could not be evaluated. It is
	// never silently collapsed into Admit or Refuse: api.Budget.Unmeasured
	// decides what happens, and either way an event names the clause.
	Indeterminate Decision = "indeterminate"
)

// ClauseState is one dimension's reading against its ceiling.
type ClauseState string

const (
	Within     ClauseState = "within"
	Exceeded   ClauseState = "exceeded"
	Unmeasured ClauseState = "unmeasured"
)

// ClauseResult is one dimension's evaluation.
type ClauseResult struct {
	Dimension api.Dimension
	// Shape is carried so the renderer reads a clause by shape rather than
	// by naming individual dimensions, the same reason overlap and repair
	// exemptions are shape properties.
	Shape api.Shape
	State ClauseState
	Used  int
	Limit int
	// Adds is what this request would add to Used. Always 0 for cumulative
	// clauses: pricing "what will K more of these cost" needs
	// usage.Baseline, which is deliberately unimplemented, and fabricating
	// an estimate from no history is the failure that contract forbids.
	Adds int
	Unit string
	// Note is why the clause is Unmeasured, or the partiality caveat on a
	// measured one.
	Note string
}

// Verdict is the admission answer.
type Verdict struct {
	Decision Decision
	// Granted is how many of Request.Want may proceed: 0, Want, or headroom
	// when Request.AllowPartial.
	Granted int
	// Want echoes the request, so Reason can say "3 of 5".
	Want int
	// Role echoes the request, so Reason can name it.
	Role     string
	Deciding api.Dimension
	Clauses  []ClauseResult
	// Window is when accounting for the team began, carried so every
	// cumulative figure marvel prints can say what span it covers.
	Window time.Time
}

// Refused reports a verdict that denies at least part of the request.
func (v Verdict) Refused() bool { return v.Decision == Refuse }

// Key is a stable composite of the decision, the deciding dimension, and
// that dimension's clause state.
//
// The reconciler latches on it so a standing refusal emits one event per
// transition instead of one per tick. That is arithmetic, not taste: the
// reconcile interval is 2s and the event ring holds 2000, so one event per
// refusal per role flushes the whole ring in about 67 minutes and erases
// every other event class. Digesting the decision rather than comparing
// Reason strings makes the invariant directly testable (identical verdicts
// produce identical keys, a change of deciding dimension re-emits) and
// stops an incidental message-formatting change from re-firing.
func (v Verdict) Key() string {
	var state ClauseState
	for _, c := range v.Clauses {
		if c.Dimension == v.Deciding {
			state = c.State
			break
		}
	}
	return string(v.Decision) + "|" + string(v.Deciding) + "|" + string(state)
}

// Reason is the one-line events.Event Message carrying the arithmetic, and
// doubles as the RPC error text. One line because that is the only shape
// events.Event has: it carries no structured payload, and extending it
// would change the JSON the events RPC returns, the CLI renderer, and the
// mrvl:// wire. The format is fixed so a later structured field is a
// mechanical lift.
func (v Verdict) Reason(t Trigger) string {
	var b strings.Builder
	switch v.Decision {
	case Refuse:
		fmt.Fprintf(&b, "refused %d of %d spawn(s)", v.Want-v.Granted, v.Want)
		if v.Role != "" {
			fmt.Fprintf(&b, " for role %s", v.Role)
		}
		fmt.Fprintf(&b, ": %s", v.decidingDetail())
		if v.Granted > 0 {
			fmt.Fprintf(&b, "; granted %d", v.Granted)
		}
	case Indeterminate:
		fmt.Fprintf(&b, "%s; admitted (on_unmeasured=%s)", v.decidingDetail(), api.UnmeasuredAdmit)
	default:
		fmt.Fprintf(&b, "within budget")
		if v.Role != "" {
			fmt.Fprintf(&b, " for role %s", v.Role)
		}
		if d := v.decidingDetail(); d != "" {
			fmt.Fprintf(&b, ": %s", d)
		}
	}
	fmt.Fprintf(&b, " (trigger=%s)", t)
	return b.String()
}

// decidingDetail renders the clause that decided, with its numbers.
func (v Verdict) decidingDetail() string {
	for _, c := range v.Clauses {
		if c.Dimension != v.Deciding {
			continue
		}
		var b strings.Builder
		switch {
		case c.State == Unmeasured:
			fmt.Fprintf(&b, "%s declared but %s", c.Dimension, c.Note)
			if v.Decision == Refuse {
				fmt.Fprintf(&b, " (on_unmeasured=%s)", api.UnmeasuredRefuse)
			}
			return b.String()
		case c.Shape == api.ShapeCount && c.Adds > 0:
			fmt.Fprintf(&b, "%d live + %d requested %s exceeds %s=%d", c.Used, c.Adds, c.Unit, c.Dimension, c.Limit)
		case c.Shape == api.ShapeCount:
			fmt.Fprintf(&b, "%d live %s against %s=%d", c.Used, c.Unit, c.Dimension, c.Limit)
		default:
			fmt.Fprintf(&b, "team spent %d %s against %s=%d", c.Used, c.Unit, c.Dimension, c.Limit)
			if !v.Window.IsZero() {
				fmt.Fprintf(&b, " (since=%s)", v.Window.Format(time.RFC3339))
			}
		}
		if c.Note != "" {
			fmt.Fprintf(&b, " (%s)", c.Note)
		}
		return b.String()
	}
	return ""
}

// Check is the admission verdict. Pure: no store, no meter, no clock, no
// I/O. The single arithmetic shared by every enforcement point, and the one
// mid-flight revocation (locus 3) will extend.
func Check(b api.Budget, s Snapshot, r Request) Verdict {
	return check(b, s, r, true)
}

// CheckSessions is the count-only entry point the reconciler uses. It is
// Check with every cumulative clause skipped, so a caller with no meter
// needs neither a Snapshot's token fields nor a usage import. See R2: the
// reconciler must never evaluate a monotonic clause.
func CheckSessions(b api.Budget, live, declared int, r Request) Verdict {
	return check(b, Snapshot{LiveSessions: live, DeclaredSessions: declared}, r, false)
}

func check(b api.Budget, s Snapshot, r Request, evalCumulative bool) Verdict {
	v := Verdict{
		Decision: Admit,
		Granted:  r.Want,
		Want:     r.Want,
		Role:     r.Role,
		Window:   s.Since,
	}
	// An undeclared budget is not a gate. Checked first so it costs one
	// boolean and nothing else.
	if !b.Declared() {
		return v
	}
	// A request that adds nothing is never refused. Keeps a scale-down out
	// of the gate: shedding sessions is a recovery move, and refusing it
	// would strand an over-budget team.
	if r.Want <= 0 {
		return v
	}

	// Registry order: count clauses before cumulative ones, so a session
	// ceiling decides before a spend ceiling.
	if b.MaxSessions > 0 {
		v.Clauses = append(v.Clauses, countClause(b.MaxSessions, s, r))
	}
	if b.MaxTokens > 0 && evalCumulative && r.Kind != Repair {
		v.Clauses = append(v.Clauses, tokenClause(b.MaxTokens, s))
	}

	// Refuse outranks Indeterminate: a measured breach is more certain than
	// an unevaluable clause.
	for _, c := range v.Clauses {
		if c.State != Exceeded {
			continue
		}
		// Partial grants are count-shaped only, read by shape rather than by
		// naming a dimension for the same reason overlap and repair
		// exemptions are: a cumulative clause has no headroom to hand out,
		// because pricing "what would K more of these cost" needs a baseline
		// that deliberately does not exist (see ClauseResult.Adds).
		granted := 0
		if r.AllowPartial && c.Shape == api.ShapeCount {
			granted = headroom(c.Limit, c.Used)
		}
		// Headroom covering the whole ask is not a refusal, and Granted stays
		// at Want here rather than rising to the headroom. Two defects lived
		// in the single assignment this replaced. Refusing on the clause alone
		// logged, emitted admission.refused at warning severity, and latched a
		// reconciler hold for a tick that spawned every requested session,
		// because Refused() reads the decision and not the count. Assigning
		// the raw headroom then handed a whole team's headroom to one role's
		// smaller deficit, so the reconciler spawned past role.Replicas and
		// deleted the excess on the next tick, printing "refused -4 of 1"
		// along the way. Granted is only ever lowered below Want, never
		// raised above it.
		if granted >= r.Want {
			v.Deciding = c.Dimension
			continue
		}
		v.Decision = Refuse
		v.Deciding = c.Dimension
		v.Granted = granted
		return v
	}
	for _, c := range v.Clauses {
		if c.State != Unmeasured {
			continue
		}
		v.Deciding = c.Dimension
		if b.Unmeasured() == api.UnmeasuredRefuse {
			v.Decision = Refuse
			v.Granted = 0
		} else {
			v.Decision = Indeterminate
			v.Granted = r.Want
		}
		return v
	}
	if v.Deciding == "" && len(v.Clauses) > 0 {
		v.Deciding = v.Clauses[0].Dimension
	}
	return v
}

// countClause evaluates the live-session ceiling. Exact and pre-spawn: it
// reads the store's own count, not a stream, so it has no absence state and
// survives a daemon restart.
func countClause(limit int, s Snapshot, r Request) ClauseResult {
	res := ClauseResult{
		Dimension: api.DimMaxSessions,
		Shape:     api.ShapeCount,
		State:     Within,
		Used:      s.LiveSessions,
		Limit:     limit,
		Adds:      r.Want,
		Unit:      "sessions",
	}
	// R5: a shift's overlapping generation is replacement, not growth.
	if r.Overlap {
		res.Note = "shift overlap does not count against a session ceiling"
		return res
	}
	// R1: converging on an already-admitted declared count cannot cross the
	// cap, because the parser enforces declared <= limit. The else arm is
	// the hole that invariant leaves: if the declaration itself is over
	// budget, more spawns are the wrong answer and an operator edit is the
	// right one.
	if r.Kind == Repair {
		if s.DeclaredSessions > limit {
			res.State = Exceeded
			res.Adds = 0
			res.Note = fmt.Sprintf("team declares %d sessions against this ceiling", s.DeclaredSessions)
		}
		return res
	}
	if s.LiveSessions+r.Want > limit {
		res.State = Exceeded
	}
	return res
}

// tokenClause evaluates the cumulative spend ceiling.
//
// Refusal on a partial total is sound because partiality can only
// understate (R3): every unobserved contributor adds zero, so the measured
// figure is a floor and spent >= limit implies true >= limit. Admission on
// a partial total is the ambiguous direction, so that is where the clause
// speaks — Within with a notice, or Unmeasured when nothing was measured at
// all.
func tokenClause(limit int, s Snapshot) ClauseResult {
	res := ClauseResult{
		Dimension: api.DimMaxTokens,
		Shape:     api.ShapeCumulative,
		State:     Within,
		Used:      s.TokensObserved,
		Limit:     limit,
		Unit:      "tokens",
	}
	if !s.TokensMetered {
		res.State = Unmeasured
		res.Used = 0
		res.Note = "no usage meter is wired to this daemon"
		return res
	}
	if s.TokensSeen == 0 && s.Since.IsZero() {
		res.State = Unmeasured
		res.Used = 0
		res.Note = "no token usage observed yet"
		return res
	}
	var notes []string
	if s.TokensPartial {
		notes = append(notes, "partial: some sessions unobserved, so this is a floor")
	}
	if s.TokensSuspect {
		notes = append(notes, "suspect: the meter reported a cumulation violation, so this may be inflated")
	}
	res.Note = strings.Join(notes, "; ")
	if s.TokensObserved >= limit {
		res.State = Exceeded
	}
	return res
}

func headroom(limit, used int) int {
	if h := limit - used; h > 0 {
		return h
	}
	return 0
}

// Row is one `marvel get budgets` line: a declared dimension, what has
// been observed against it, and that dimension's state.
type Row struct {
	Workspace string        `json:"workspace"`
	Team      string        `json:"team"`
	Dimension api.Dimension `json:"dimension"`
	Limit     int           `json:"limit"`
	Observed  int           `json:"observed"`
	Headroom  int           `json:"headroom"`
	State     string        `json:"state"`
	Window    time.Time     `json:"window,omitempty"`
	Note      string        `json:"note,omitempty"`
}

// Row states.
const (
	RowOK = "ok"
	// RowAtCeiling is a count dimension with no headroom left for growth.
	// Distinct from RowRefusing because it is the NORMAL state of a healthy
	// team: the declaration clause pushes an operator toward
	// sum(replicas) == max_sessions, and repair toward an already-declared
	// count is exempt, so such a team sits here forever and refuses nothing.
	// Reading it as a refusal made "refusing" the resting state and left the
	// only which-dimension-tripped surface unable to tell a real refusal from
	// the intended configuration.
	RowAtCeiling = "at-ceiling"
	// RowRefusing means a refusal is standing right now. Read from
	// api.Team.Admission, the condition the reconciler recomputes every tick
	// and clears the moment the gate admits, rather than inferred from
	// headroom. Only the count clause can produce it: the reconciler
	// evaluates count clauses alone (R2), and a synchronous verb's refusal
	// changes nothing and so is not a standing condition.
	RowRefusing  = "refusing"
	RowUnmetered = "unmetered"
)

// Rows assembles the diagnostic view for one team: one row per declared
// dimension, none for a team that declares no budget. Evaluated straight
// from the snapshot rather than from a Verdict, because the operator's
// question ("which dimension tripped and by how much") is per dimension
// and does not depend on any pending request.
//
// "Is it refusing?" is read from the team's standing condition, never
// inferred from zero headroom: a team at its declared ceiling refuses
// nothing, and a shift legitimately reads above it. See RowAtCeiling.
func Rows(t api.Team, s Snapshot) []Row {
	if !t.Budget.Declared() {
		return nil
	}
	var out []Row
	for _, spec := range api.Specs() {
		limit, ok := t.Budget.Limit(spec.Dimension)
		if !ok {
			continue
		}
		row := Row{
			Workspace: t.Workspace,
			Team:      t.Name,
			Dimension: spec.Dimension,
			Limit:     limit,
			State:     RowOK,
		}
		switch spec.Dimension {
		case api.DimMaxSessions:
			row.Observed = s.LiveSessions
			row.Headroom = headroom(limit, s.LiveSessions)
			var notes []string
			switch {
			case t.Admission.Held:
				row.State = RowRefusing
				notes = append(notes, t.Admission.Reason)
			case row.Headroom == 0:
				row.State = RowAtCeiling
			}
			// A rotation runs the new generation beside the old and a count
			// ceiling exempts that overlap (R5), so Observed can read above
			// Limit until draining finishes. Say which mechanism produced the
			// overshoot rather than leaving the operator with two numbers that
			// cannot both be right.
			if t.Shift.Phase != api.ShiftNone {
				notes = append(notes, fmt.Sprintf("shift %s: the new generation overlaps the old, which a session ceiling exempts, so live may read above the limit until draining finishes", t.Shift.Phase))
			}
			row.Note = strings.Join(notes, "; ")
		case api.DimMaxTokens:
			c := tokenClause(limit, s)
			row.Observed = c.Used
			row.Headroom = headroom(limit, c.Used)
			row.Note = c.Note
			row.Window = s.Since
			switch c.State {
			case Exceeded:
				row.State = RowRefusing
			case Unmeasured:
				row.State = RowUnmetered
			}
		}
		out = append(out, row)
	}
	return out
}
