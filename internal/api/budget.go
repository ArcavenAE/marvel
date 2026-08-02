package api

import (
	"slices"
	"sort"
	"strings"
	"time"
)

// Dimension names one row of the agentic resource matrix expressed as a
// budget clause. See aae-orc-qiay.
type Dimension string

const (
	DimMaxSessions Dimension = "max_sessions"
	DimMaxTokens   Dimension = "max_tokens"
	// The three below are registered, not enforced. Named here so a
	// manifest declaring one gets "known dimension, not implemented,
	// owner X" rather than silence, and so the matrix rows still awaiting
	// an evaluator are visible in one place.
	DimMaxCostUSD       Dimension = "max_cost_usd"
	DimMaxTeamRSSBytes  Dimension = "max_team_rss_bytes"
	DimMaxSessionCtxPct Dimension = "max_session_ctx_percent"
)

// Shape decides how a clause reads its meter, whether a shift's
// overlapping generation counts against it, and whether repair is exempt.
// Adopting shape rather than per-clause special cases is what lets a
// third dimension drop in without re-deciding overlap semantics.
type Shape string

const (
	// ShapeCount is a level over live sessions: it falls when sessions
	// exit, a shift's overlapping generation does not count, and repair
	// toward already-declared replicas is exempt (the declaration clause
	// makes declared <= limit an invariant, so converging on it cannot
	// cross the cap).
	ShapeCount Shape = "count"
	// ShapeCumulative accumulates and never falls within a daemon
	// lifetime. A new generation is a new spender, so an overlap counts,
	// and it is never evaluated on the repair path: gating repair on a
	// monotonic meter is a permanent outage.
	ShapeCumulative Shape = "cumulative"
	// ShapeLevel is a per-session reading with no team aggregate. No
	// dimension of this shape is implemented; context occupancy is a
	// level and never a sum (see internal/usage/doc.go), and how it
	// aggregates across a team belongs to the shift-trigger work.
	ShapeLevel Shape = "level"
)

// Spec is the registry entry for one dimension.
type Spec struct {
	Dimension   Dimension
	Shape       Shape
	Unit        string
	Integral    bool
	Implemented bool
	MatrixRow   int
	// Owner names the bd ticket or lane that owns an unimplemented row,
	// so the validation error points somewhere.
	Owner string
}

// budgetSpecs is the dimension registry, in the order Specs reports and
// clauses evaluate: count-shaped rows before cumulative ones, so a
// session ceiling decides before a spend ceiling.
var budgetSpecs = []Spec{
	{Dimension: DimMaxSessions, Shape: ShapeCount, Unit: "sessions", Integral: true, Implemented: true, MatrixRow: 12, Owner: "aae-orc-qiay"},
	{Dimension: DimMaxTokens, Shape: ShapeCumulative, Unit: "tokens", Integral: true, Implemented: true, MatrixRow: 2, Owner: "aae-orc-qiay"},
	{Dimension: DimMaxCostUSD, Shape: ShapeCumulative, Unit: "usd", Integral: false, Implemented: false, MatrixRow: 2, Owner: "aae-orc-qiay follow-on"},
	{Dimension: DimMaxTeamRSSBytes, Shape: ShapeCount, Unit: "bytes", Integral: true, Implemented: false, MatrixRow: 12, Owner: "aae-orc-hpeu"},
	{Dimension: DimMaxSessionCtxPct, Shape: ShapeLevel, Unit: "percent", Integral: false, Implemented: false, MatrixRow: 1, Owner: "aae-orc-hpeu"},
}

// Specs returns the dimension registry in evaluation order. A copy, so a
// caller cannot reorder the registry for everyone else.
func Specs() []Spec { return slices.Clone(budgetSpecs) }

// LookupDimension returns the registry entry for a dimension.
func LookupDimension(d Dimension) (Spec, bool) {
	for _, s := range budgetSpecs {
		if s.Dimension == d {
			return s, true
		}
	}
	return Spec{}, false
}

// DimensionList returns every registered dimension sorted and
// comma-joined, for inclusion in validation error messages. Mirrors
// permissionModeList.
func DimensionList() string {
	names := make([]string, 0, len(budgetSpecs))
	for _, s := range budgetSpecs {
		names = append(names, string(s.Dimension))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// UnmeasuredMode is what a declared clause does when the meter cannot
// answer.
//
// The default is admit: the operator declared a budget on a dimension,
// not "refuse when the meter cannot answer", and failing closed on absent
// data is a gate they did not ratify (ADR-007 clause 3, SOUL.md section
// 8). Declaring refuse IS that ratification, so the fail-closed posture
// stays available to an operator who wants it. Either way the degradation
// is audible: an admitted-but-unmeasured clause emits an event naming the
// clause and the reason.
type UnmeasuredMode string

const (
	UnmeasuredAdmit  UnmeasuredMode = "admit"
	UnmeasuredRefuse UnmeasuredMode = "refuse"
)

// canonicalUnmeasuredModes is the accepted set, checked at parse time so
// a typo does not silently resolve to the default.
var canonicalUnmeasuredModes = map[UnmeasuredMode]bool{
	UnmeasuredAdmit:  true,
	UnmeasuredRefuse: true,
}

// unmeasuredModeList returns the valid modes sorted and comma-joined.
func unmeasuredModeList() string {
	modes := make([]string, 0, len(canonicalUnmeasuredModes))
	for m := range canonicalUnmeasuredModes {
		modes = append(modes, string(m))
	}
	sort.Strings(modes)
	return strings.Join(modes, ", ")
}

// Budget is a team's declared resource ceiling.
//
// Every field is zero-means-unset: a team with no budget block declares
// no gate, and marvel's behavior for it is identical to before this field
// existed. A nonzero field is the operator's ratification instrument
// (ADR-007 clause 3, SOUL.md section 8) and the only thing that turns a
// metered value into a refusal. Nothing here is inferred from a metric,
// and no default ceiling exists. See aae-orc-qiay.
type Budget struct {
	// MaxSessions caps live sessions across the whole team: every role,
	// plus ad-hoc sessions attributed to the team. Counted from the store
	// rather than a stream, so it is exact before a spawn and survives a
	// daemon restart.
	//
	// A rolling shift is the one exemption, and it is deliberate: the new
	// generation runs beside the old until draining finishes, so live can
	// reach twice a role's replicas for the length of the rotation and the
	// ceiling does not refuse it (replacement is not growth; see
	// admission R5, and Request.Overlap). An operator sizing this against a
	// hard external concurrency quota should size for that overlap, or
	// avoid shifting a team that sits at its ceiling.
	MaxSessions int `toml:"max_sessions,omitempty"  json:"max_sessions,omitempty"`
	// MaxTokens caps the team's layout-normalized prompt, output, and
	// reasoning tokens SINCE ACCOUNTING BEGAN. The accountant is
	// in-memory, so this window restarts with the daemon and with
	// `marvel daemon reexec`; every figure marvel prints for it carries
	// that window. No class weighting: marvel cannot see a provider's
	// discount schedule, so any weighting would be invented.
	MaxTokens int `toml:"max_tokens,omitempty"    json:"max_tokens,omitempty"`
	// OnUnmeasured is what a declared clause does when the meter cannot
	// answer. Empty resolves to UnmeasuredAdmit.
	OnUnmeasured UnmeasuredMode `toml:"on_unmeasured,omitempty" json:"on_unmeasured,omitempty"`
}

// Declared reports whether any enforceable clause is set. Callers check
// this first so an undeclared budget costs nothing: no store read, no
// meter read, no event.
func (b Budget) Declared() bool {
	return b.MaxSessions > 0 || b.MaxTokens > 0
}

// Unmeasured resolves the default.
func (b Budget) Unmeasured() UnmeasuredMode {
	if b.OnUnmeasured == UnmeasuredRefuse {
		return UnmeasuredRefuse
	}
	return UnmeasuredAdmit
}

// Limit returns the declared ceiling for an implemented dimension. ok is
// false for an unset clause and for any dimension this slice does not
// enforce.
func (b Budget) Limit(d Dimension) (int, bool) {
	switch d {
	case DimMaxSessions:
		return b.MaxSessions, b.MaxSessions > 0
	case DimMaxTokens:
		return b.MaxTokens, b.MaxTokens > 0
	}
	return 0, false
}

// AdmissionState is the status half of Budget: the standing condition the
// reconciler recomputes every tick, surfaced by `marvel describe team`.
//
// Status on a spec record follows the Team.Shift precedent exactly
// (toml:"-", written through UpdateTeam on transitions only, never on
// every tick, so there is no bolt write storm). A copy rehydrated stale
// from bolt is corrected within one reconcile tick, because the condition
// is derived from live state rather than remembered.
type AdmissionState struct {
	Held   bool      `json:"held,omitempty"`
	Role   string    `json:"role,omitempty"`
	Reason string    `json:"reason,omitempty"`
	Since  time.Time `json:"since,omitempty"`
}

// CountAlive returns how many of these sessions count toward replicas.
// Shared by the reconciler and the daemon so both compute "live"
// identically.
func CountAlive(sessions []Session) int {
	n := 0
	for i := range sessions {
		if sessions[i].State.CountsAsAlive() {
			n++
		}
	}
	return n
}
