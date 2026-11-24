package api

import (
	"fmt"
	"regexp"
	"slices"
)

// Duties a role declares (docs/design/seat-readiness.md section 3). A duty
// names a check from a fixed library, and a role cannot invent one: the
// library below is the whole set. Growing it is a one-line edit made with a
// design ruling, never a manifest input, the same rule as the readiness Source
// vocabulary.
//
// The checks themselves are not here. This file says which names a role may
// declare; a check that proves a duty is built separately (aae-orc-lxb9b).

// The duty names. DutyFirstTurn and DutyAck are the duties every seat marvel
// spawns carries, whatever its role declares (config.ResolvedDuties).
const (
	DutyFirstTurn = "first-turn"
	DutyAck       = "ack"
	DutyGHRead    = "gh.read"
	DutyGHComment = "gh.comment"
	DutyGHPost    = "gh.post"
	DutyJiraRead  = "jira.read"
	DutyJiraWrite = "jira.write"
)

// dutyLibrary is the fixed set of duty names, sorted.
var dutyLibrary = []string{
	DutyAck,
	DutyFirstTurn,
	DutyGHComment,
	DutyGHPost,
	DutyGHRead,
	DutyJiraRead,
	DutyJiraWrite,
}

// DutyNames returns the fixed duty library, sorted. The slice is a copy.
func DutyNames() []string { return slices.Clone(dutyLibrary) }

// IsDuty reports whether name is in the library. Names compare exactly.
func IsDuty(name string) bool {
	_, found := slices.BinarySearch(dutyLibrary, name)
	return found
}

// validateDuties refuses a duties list a check could not serve: an empty entry,
// a name outside the library, or a name twice. A name outside the library is
// refused by position and never echoed, so a value pasted into the wrong key
// cannot reach a log through the error. A library name is safe to print.
func validateDuties(where string, duties []string) error {
	seen := make(map[string]bool, len(duties))
	for k, d := range duties {
		if d == "" {
			return fmt.Errorf("%s.duties[%d]: an entry is empty", where, k)
		}
		if !IsDuty(d) {
			return fmt.Errorf("%s.duties[%d] is not a duty in the library (valid: %v)", where, k, dutyLibrary)
		}
		if seen[d] {
			return fmt.Errorf("%s.duties: %q appears twice", where, d)
		}
		seen[d] = true
	}
	return nil
}

var seatSecretNeedRE = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// validateSeatSecrets checks the needs a role names. A need is a short name the
// cluster maps to a variable, never a value, so a refusal quotes no entry.
func validateSeatSecrets(where string, needs []string) error {
	seen := make(map[string]bool, len(needs))
	for k, n := range needs {
		if !seatSecretNeedRE.MatchString(n) {
			return fmt.Errorf("%s.seat_secrets[%d] is not a need name (lower case letters, digits, - and _, starting with a letter)", where, k)
		}
		if seen[n] {
			return fmt.Errorf("%s.seat_secrets[%d] repeats an earlier entry", where, k)
		}
		seen[n] = true
	}
	return nil
}
