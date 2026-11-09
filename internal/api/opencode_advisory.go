package api

import "fmt"

// OpenCodeAdvisories lists the roles on opencode that declare something the
// harness does not read: a heartbeat healthcheck, which opencode has no feed
// for, and runtime.context_window, which replaces the model catalog the seat
// would otherwise read its window from. Both are advisory and never a refusal:
// an existing manifest that carries either must still apply.
//
// Owner reading, proposed for review before merge: the wording of both lines.
func (m *Manifest) OpenCodeAdvisories() []string {
	var out []string
	for _, t := range m.Teams {
		for _, r := range t.Roles {
			if r.Runtime.AdapterName() != "opencode" {
				continue
			}
			if r.HealthCheck != nil && HealthCheckType(r.HealthCheck.Type) == HealthCheckHeartbeat {
				out = append(out, fmt.Sprintf("team %s role %s: opencode has no heartbeat feed, so healthcheck type \"heartbeat\" is not fed; advisory only", t.Name, r.Name))
			}
			if r.Runtime.ContextWindow > 0 {
				out = append(out, fmt.Sprintf("team %s role %s: runtime.context_window on opencode replaces the model catalog the seat would read its window from; advisory only", t.Name, r.Name))
			}
		}
	}
	return out
}
