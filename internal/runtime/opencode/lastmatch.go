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
	// allow, deny or no match, and the verify step treats it as a failure.
	Unverified
)

// LastMatch returns the rule that decides permission and pattern: opencode
// evaluates the merged list in order and the last rule whose permission and
// pattern both match wins, so a later rule overrides an earlier one. Both
// fields are compared as strings, exactly as given, so the caller supplies the
// form opencode evaluates: an external_directory pattern is absolute with its
// leading slash, and a read path is evaluated without it.
//
// A rule that could be the last match but uses syntax outside the measured
// subset (see globMatch) makes the result Unverified, not a guess.
func LastMatch(rules []Rule, permission, pattern string) (Rule, Result) {
	for i := len(rules) - 1; i >= 0; i-- {
		r := rules[i]
		pm := globMatch(r.Permission, permission)
		xm := globMatch(r.Pattern, pattern)
		switch {
		case pm == kindNo || xm == kindNo:
			continue
		case pm == kindUnknown || xm == kindUnknown:
			return Rule{}, Unverified
		}
		return r, Decided
	}
	return Rule{}, NoRule
}

type kind int

const (
	kindNo kind = iota
	kindYes
	kindUnknown
)

// unmeasuredSyntax holds the glob characters nothing measured on opencode
// 1.18.34 covers: character classes, braces, negation and escapes.
const unmeasuredSyntax = `[]{}!\`

// globMatch reports whether target matches the rule pattern g, for the forms
// measured on opencode 1.18.34 and nothing wider:
//
//   - `*` matches any run of characters, including none and including `/`;
//   - `**` behaved the same as `*` in every probe, and is read as `*`;
//   - `?` matches exactly one character;
//   - a match is anchored at both ends and case-sensitive;
//   - every other character matches itself.
//
// A pattern holding a character class, a brace, a negation or an escape is
// unknown, and so is a `?` against a target with a non-ASCII character, since
// one character was only measured for ASCII.
func globMatch(g, target string) kind {
	if strings.ContainsAny(g, unmeasuredSyntax) {
		return kindUnknown
	}
	if strings.Contains(g, "?") && !isASCII(target) {
		return kindUnknown
	}
	if wildcard(g, target) {
		return kindYes
	}
	return kindNo
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// wildcard matches s against g with `*` (any run) and `?` (one byte), by the
// usual backtracking over the most recent star.
func wildcard(g, s string) bool {
	gi, si := 0, 0
	star, mark := -1, 0
	for si < len(s) {
		switch {
		case gi < len(g) && g[gi] == '*':
			star, mark = gi, si
			gi++
		case gi < len(g) && (g[gi] == '?' || g[gi] == s[si]):
			gi++
			si++
		case star >= 0:
			mark++
			gi, si = star+1, mark
		default:
			return false
		}
	}
	for gi < len(g) && g[gi] == '*' {
		gi++
	}
	return gi == len(g)
}
