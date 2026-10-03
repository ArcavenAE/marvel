package config

import "github.com/arcavenae/marvel/internal/api"

// ResolvedGlobalRole is the one answer to "which global role does this role
// hold": the declared word, or the default by name when none is declared, and
// only when the cluster admits the role's name. It returns "" for a role with
// no global role. Every reader (the renderer, Credential, the seat env) asks
// here, with the admitted set the daemon loaded; none compares a role name.
// Scaffold: returns none until the resolver lands.
func ResolvedGlobalRole(role api.Role, admitted []string) string {
	_, _, _ = role, admitted, DefaultGlobalRoleByName
	return ""
}
