package api

import (
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/asof"
)

// Readiness routing (docs/design/seat-readiness.md section 6, ruled
// 2026-10-10 in section 9): which seats a job may be placed on, read from the
// duty.<name> and withdrawn.<duty> cells and nothing else. The rule is a pure
// function so that director and marvel route identically. The golden vectors
// in contracts/readiness/routing-vectors.json are the shared test.
//
// RouteSeat reads cells and a clock. It reads no count, rate or time
// threshold, so none of them can exclude a seat (ADR-007): the clock only
// decides whether a cell is fresh, and an expired cell asks first.

// RouteVerdict is the router's answer for a seat, or for one duty of it.
type RouteVerdict string

const (
	// RouteExcluded: off the free list, with the reason shown (rule 1).
	RouteExcluded RouteVerdict = "excluded"
	// RouteEligible: a fresh proven value for every duty the job needs (rule 2).
	RouteEligible RouteVerdict = "eligible"
	// RouteAskFirst: neither excluded nor proven; never assumed (rule 3).
	RouteAskFirst RouteVerdict = "ask-first"
)

// RouteReason is the code for a duty's verdict. It is a fixed word, never
// pane text or an error string.
type RouteReason string

const (
	ReasonWithdrawn      RouteReason = "withdrawn"       // withdrawn.<duty> is set
	ReasonStructuralFail RouteReason = "structural-fail" // a fresh fail with a structural code
	ReasonProven         RouteReason = "proven"          // a fresh proven value
	ReasonHostVantage    RouteReason = "host-vantage"    // proven from a host vantage proves the host
	ReasonTransientFail  RouteReason = "transient-fail"  // a fresh fail with a non-structural code
	ReasonUnproven       RouteReason = "unproven"        // a fresh unproven value
	ReasonNeverObserved  RouteReason = "never-observed"  // the cell reads -
	ReasonExpired        RouteReason = "expired"         // the cell reads ?
	ReasonOverridden     RouteReason = "overridden"      // excluded, and a supervisor named the seat
	ReasonNoDuties       RouteReason = "no-duties"       // the job named no duty to judge
)

// RouteOverride is a supervisor naming a seat for a job (rule 1, ruling 3).
// It applies only to the seat it names, and only when By names who.
type RouteOverride struct {
	Seat string `json:"seat"`
	By   string `json:"by"`
}

// RouteInput is everything the rule reads for one seat and one job.
type RouteInput struct {
	Seat string `json:"seat"`
	// Duties are the duties the job needs, as the role resolver gives them.
	// For a supervised seat the caller includes first-turn and ack (design
	// section 3).
	Duties []string `json:"duties"`
	// Cells are the seat's readiness cells, keyed by cell name, as
	// Store.SeatReadiness returns them.
	Cells    map[string]asof.Cell[string] `json:"cells"`
	Override *RouteOverride               `json:"override,omitempty"`
}

// DutyRoute is the verdict for one duty, with the cell and Source behind it.
type DutyRoute struct {
	Duty    string       `json:"duty"`
	Verdict RouteVerdict `json:"verdict"`
	Reason  RouteReason  `json:"reason"`
	Source  string       `json:"source,omitempty"`
}

// RouteDecision is the router's answer for a seat.
type RouteDecision struct {
	Verdict RouteVerdict `json:"verdict"`
	// Reason is set when the seat-level verdict has a reason of its own
	// (no-duties); otherwise each duty carries its reason.
	Reason       RouteReason `json:"reason,omitempty"`
	Duties       []DutyRoute `json:"duties,omitempty"`
	OverriddenBy string      `json:"overridden_by,omitempty"`
}

// RouteSeat applies the routing rule to one seat at now. Each duty the job
// needs gets a verdict; the seat is excluded if any duty is, eligible only if
// every duty is, and asks first otherwise. A supervisor override that names
// this seat lifts each exclusion to ask first, never to eligible: naming a
// seat is a choice to ask it, not evidence that the duty works.
func RouteSeat(in RouteInput, now time.Time) RouteDecision {
	if len(in.Duties) == 0 {
		return RouteDecision{Verdict: RouteAskFirst, Reason: ReasonNoDuties}
	}
	override := in.Override != nil && in.Override.By != "" && in.Override.Seat == in.Seat
	out := RouteDecision{Verdict: RouteEligible, Duties: make([]DutyRoute, 0, len(in.Duties))}
	lifted := false
	for _, duty := range in.Duties {
		d := routeDuty(duty, in.Cells, now)
		if d.Verdict == RouteExcluded && override {
			d.Verdict, d.Reason = RouteAskFirst, ReasonOverridden
			lifted = true
		}
		out.Duties = append(out.Duties, d)
		switch {
		case d.Verdict == RouteExcluded:
			out.Verdict = RouteExcluded
		case d.Verdict == RouteAskFirst && out.Verdict == RouteEligible:
			out.Verdict = RouteAskFirst
		}
	}
	if lifted {
		out.OverriddenBy = in.Override.By
	}
	return out
}

// routeDuty is the verdict for one duty. A withdrawal is read first and
// never expires. Then the duty cell: fresh structural fail excludes, fresh
// proven from the seat's own vantage is eligible, and everything else asks
// first.
func routeDuty(duty string, cells map[string]asof.Cell[string], now time.Time) DutyRoute {
	if w, ok := cells[WithdrawnCellName(duty)]; ok && w.Value == string(DutyWithdrawn) && !w.ObservedAt.IsZero() {
		return DutyRoute{Duty: duty, Verdict: RouteExcluded, Reason: ReasonWithdrawn, Source: w.Source}
	}
	c := cells[DutyCellName(duty)]
	d := DutyRoute{Duty: duty, Verdict: RouteAskFirst, Source: c.Source}
	switch c.State(now) {
	case asof.None:
		d.Reason = ReasonNeverObserved
	case asof.Stale:
		d.Reason = ReasonExpired
	default:
		switch DutyState(c.Value) {
		case DutyProven:
			if dutySourceVantage(c.Source) == "host" {
				d.Reason = ReasonHostVantage
			} else {
				d.Verdict, d.Reason = RouteEligible, ReasonProven
			}
		case DutyFail:
			if structuralDutySource(c.Source) {
				d.Verdict, d.Reason = RouteExcluded, ReasonStructuralFail
			} else {
				d.Reason = ReasonTransientFail
			}
		default:
			d.Reason = ReasonUnproven
		}
	}
	return d
}

// dutySourceVantage is the vantage word of a Source.
func dutySourceVantage(src string) string {
	v, _, _ := strings.Cut(src, ":")
	return v
}

// matchedDialogClasses are the dialog pattern classes. A pane:dialog code is a
// matched dialog only when it names one of them; any other code there
// (no-dialog-pattern, unknown, or a code from another stage) is not a match
// (design section 4, "a held dialog"), so it never excludes.
var matchedDialogClasses = map[string]bool{"consent": true, "trust": true, "folder-trust": true, "logged-out": true}

// structuralDutySource reports whether a fail's Source is one of the codes
// that may exclude (design section 4, "Structural codes"): the far side
// refused the credential, the watcher saw a denial, or a dialog pattern
// matched. Every reach code, rate-limited, and anything under dialog that is
// not a matched pattern class are transient.
func structuralDutySource(src string) bool {
	switch src {
	case "seat:auth:auth-rejected", "seat:auth:forbidden":
		return true
	}
	parts := strings.Split(src, ":")
	if len(parts) < 2 {
		return false
	}
	switch parts[1] {
	case "denial":
		return parts[0] == "seat" || parts[0] == "pane"
	case "dialog":
		return parts[0] == "pane" && len(parts) == 3 && matchedDialogClasses[parts[2]]
	}
	return false
}
