package opencode

import "strings"

// Rule is one entry of an opencode agent's merged permission rule list, in the
// shape `opencode debug agent <name>` prints it.
type Rule struct {
	Permission string `json:"permission"`
	Pattern    string `json:"pattern"`
	Action     string `json:"action"`
}

// Result says what LastMatch found.
type Result int

const (
	// Decided: a rule decides the question; LastMatch returned it.
	Decided Result = iota
	// NoRule: no rule matches. No default is invented here, because the
	// caller's question ("is the last match ask?") needs the absence kept
	// apart from every action.
	NoRule
	// Unverified: a rule uses glob syntax whose meaning in opencode is not
	// measured, so whether it matches cannot be told. It is never read as
	// allow, deny or no match.
	Unverified
)

// LastMatch returns the rule that decides permission and pattern: opencode
// evaluates the merged list in order and the last rule whose permission and
// pattern both match wins, so a later rule overrides an earlier one. When a
// rule that could be the last match uses syntax outside the measured subset
// (see matchKind), the result is Unverified, not a guess.
//
// STUB: returns the first match.
func LastMatch(rules []Rule, permission, pattern string) (Rule, Result) {
	for _, r := range rules {
		pm := matchKind(r.Permission, permission)
		xm := matchKind(r.Pattern, pattern)
		if pm == kindUnknown || xm == kindUnknown {
			return Rule{}, Unverified
		}
		if pm == kindYes && xm == kindYes {
			return r, Decided
		}
	}
	return Rule{}, NoRule
}

type kind int

const (
	kindNo kind = iota
	kindYes
	kindUnknown
)

// matchKind reports whether target matches the rule pattern g, for the glob
// syntax the recorded rule order measures and nothing wider:
//
//   - a literal, which matches itself;
//   - `*` alone, which matches every target;
//   - `<prefix>/*`, which matches a target made of the prefix, a slash and one
//     further segment with no slash in it.
//
// Any other `*`, a `?`, and a target that carries a further slash under
// `<prefix>/*` are unknown: nothing recorded says what opencode does with them.
func matchKind(g, target string) kind {
	if g == "*" {
		return kindYes
	}
	if !strings.ContainsAny(g, "*?") {
		if g == target {
			return kindYes
		}
		return kindNo
	}
	prefix, ok := strings.CutSuffix(g, "/*")
	if !ok || strings.ContainsAny(prefix, "*?") {
		return kindUnknown
	}
	rest, under := strings.CutPrefix(target, prefix+"/")
	switch {
	case !under || rest == "":
		return kindNo
	case strings.Contains(rest, "/"):
		return kindUnknown
	}
	return kindYes
}
