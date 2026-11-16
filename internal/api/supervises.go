package api

import "fmt"

// validateSupervises checks a role's supervises list as parsed: every name is
// one team address token, none is the role's own team, and none repeats. That
// the named teams exist is not checked here. It is resolved at every broker
// render against the applied teams of the supervisor's workspace, so a
// manifest may be applied before the teams it names (aae-orc-spk60).
func validateSupervises(team string, names []string) error {
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		switch {
		case !nameClass.MatchString(n):
			return fmt.Errorf("%q is not an addressable team name", n)
		case n == team:
			return fmt.Errorf("%q is the role's own team", n)
		case seen[n]:
			return fmt.Errorf("%q is listed twice", n)
		}
		seen[n] = true
	}
	return nil
}
