package api

import "fmt"

// PermissionRefusal reports why a role's harness has no mapping for the
// role's permissions, or "" when it has one or needs none. Injected for the
// reason HandoffToolCapableRole is: the answer lives in the adapter registry.
type PermissionRefusal func(ManifestRole) string

// PermissionRefusals lists every role whose permissions its harness has no
// ruled mapping for. apply refuses the manifest on a non-empty list: the role
// would otherwise run with the harness defaults and nobody would be told
// (marvel#385).
func (m *Manifest) PermissionRefusals(refuse PermissionRefusal) []string {
	if refuse == nil {
		return nil
	}
	var out []string
	for _, t := range m.Teams {
		for _, r := range t.Roles {
			why := refuse(r)
			if why == "" {
				continue
			}
			runtimeName := r.Runtime.Image
			if runtimeName == "" {
				runtimeName = r.Runtime.Command
			}
			out = append(out, fmt.Sprintf("team %s role %s runtime %q: %s", t.Name, r.Name, runtimeName, why))
		}
	}
	return out
}
