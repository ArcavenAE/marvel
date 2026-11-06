package api

import "fmt"

// HandoffToolCapableRole reports whether a role's harness can be given the
// submit_handoff tool: an adapter with a seeder, or a channel the role's
// environment declares, that can offer it. Injected for the reason
// ContextFeedCapableRole is: the answer lives in the adapter registry.
type HandoffToolCapableRole func(ManifestRole) bool

// HandoffViaRefusals lists every role that declares handoff_via = "tool" on a
// harness that cannot be given the tool. apply refuses the manifest on a
// non-empty list: the role would otherwise be sent a notice naming a tool its
// seat cannot call, and escalate at the end of the window
// (docs/design/read-only-seat-handoff.md section 6).
func (m *Manifest) HandoffViaRefusals(can HandoffToolCapableRole) []string {
	if can == nil {
		return nil
	}
	var out []string
	for _, t := range m.Teams {
		for _, r := range t.Roles {
			if r.Shift == nil || r.Shift.HandoffVia != ShiftHandoffViaTool || can(r) {
				continue
			}
			runtimeName := r.Runtime.Image
			if runtimeName == "" {
				runtimeName = r.Runtime.Command
			}
			out = append(out, fmt.Sprintf("team %s role %s: shift.handoff_via = %q needs a harness that can be given the submit_handoff tool, and runtime %q cannot be",
				t.Name, r.Name, ShiftHandoffViaTool, runtimeName))
		}
	}
	return out
}
