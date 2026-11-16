package bus

// AppliedRole is one applied role as the supervises grant sees it: its name
// and the team names its manifest says it supervises.
type AppliedRole struct {
	Name       string
	Supervises []string
}

// AppliedTeam is one applied team as the supervises grant sees it.
type AppliedTeam struct {
	Workspace string
	Team      string
	Roles     []AppliedRole
}

// SupervisedGrants returns, keyed by broker user name (`<team>.<role>`), the
// exact role-inbox subjects each role may publish to on the local bus because
// of a supervises relation. A supervisor role gets the inbox of every role of
// each team it names; every role of a named team gets the supervisor role's
// inbox and nothing else. A named team that is not applied in the
// supervisor's workspace contributes nothing.
func SupervisedGrants(applied []AppliedTeam) (map[string][]string, error) {
	return nil, nil
}

// UnresolvedSupervises lists, as "<ws>/<team>.<role> -> <name>", each named
// team that is not applied in the supervisor's workspace, sorted.
func UnresolvedSupervises(applied []AppliedTeam) []string {
	return nil
}
