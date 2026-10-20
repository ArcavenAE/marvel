package config

import (
	"slices"

	"github.com/arcavenae/marvel/internal/api"
)

// baseDuties are the duties every seat marvel spawns carries, in this order,
// whatever its role declares.
var baseDuties = []string{api.DutyFirstTurn, api.DutyAck}

// ResolvedDuties is the one answer to "which duties does this role's seat
// carry" (docs/design/seat-readiness.md section 3). A reader of a role's
// duties asks here and does not read Role.Duties directly. No reader exists
// yet; the first will be spawn's default-deny write (aae-orc-0m1lo).
//
// The answer is first-turn and ack, then the role's declared duties in the
// order declared, each once. The design gives first-turn and ack to "every
// supervised seat" and does not define the word; this reads it as every seat
// marvel spawns, so no seat is left without them. A name outside the duty
// library is dropped: parse refuses one, but a role built in code or read from
// an older store has not been through parse. The slice is the caller's.
func ResolvedDuties(role api.Role) []string {
	out := slices.Clone(baseDuties)
	for _, d := range role.Duties {
		if !api.IsDuty(d) || slices.Contains(out, d) {
			continue
		}
		out = append(out, d)
	}
	return out
}
