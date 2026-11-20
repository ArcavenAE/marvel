package api

// PermissionRefusal reports why a role's harness has no mapping for the
// role's permissions, or "" when it has one or needs none. Injected for the
// reason HandoffToolCapableRole is: the answer lives in the adapter registry.
type PermissionRefusal func(ManifestRole) string

// PermissionRefusals is a stub until the check is written.
func (m *Manifest) PermissionRefusals(_ PermissionRefusal) []string {
	return nil
}
