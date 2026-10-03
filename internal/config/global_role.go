package config

import (
	"slices"

	"github.com/arcavenae/marvel/internal/api"
)

// ResolvedGlobalRole is the one answer to "which global role does this role
// hold": the declared word, or the default by name when none is declared, and
// only when the cluster admits the role's name. It returns "" for a role with
// no global role. Every reader (the renderer, Credential, the seat env) asks
// here, with the admitted set the daemon loaded; none compares a role name and
// none reads the stored declaration directly.
//
// A declaration widens a role only when two keys agree: the manifest declares
// it and the operator's config admits the name (global_roles on the bus entry).
// The role named supervisor is admitted without being listed, which keeps
// today's behavior. "none", an unset default and any word other than the
// supervisor word all resolve to no global role.
func ResolvedGlobalRole(role api.Role, admitted []string) string {
	word := role.GlobalRole
	if word == "" {
		word = DefaultGlobalRoleByName[role.Name]
	}
	if word != api.GlobalRoleSupervisor {
		return ""
	}
	if role.Name != api.GlobalRoleSupervisor && !slices.Contains(admitted, role.Name) {
		return ""
	}
	return word
}
