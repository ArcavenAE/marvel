package bus

import (
	"fmt"
	"slices"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

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
// supervisor's workspace contributes nothing, so a name that does not resolve
// yet grants nothing and becomes live at the render after its team is applied.
//
// Only role inboxes are granted: an agent id changes at every shift and a
// wildcard over agent ids is not granted to anyone (aae-orc-spk60).
func SupervisedGrants(applied []AppliedTeam) (map[string][]string, error) {
	byName := make(map[[2]string]AppliedTeam, len(applied))
	for _, t := range applied {
		byName[[2]string{t.Workspace, t.Team}] = t
	}
	out := map[string][]string{}
	add := func(user, subject string) {
		if !slices.Contains(out[user], subject) {
			out[user] = append(out[user], subject)
		}
	}
	for _, sup := range applied {
		for _, r := range sup.Roles {
			for _, name := range r.Supervises {
				if err := checkToken("supervises team", name); err != nil {
					return nil, fmt.Errorf("team %s/%s role %s: %w", sup.Workspace, sup.Team, r.Name, err)
				}
				named, ok := byName[[2]string{sup.Workspace, name}]
				if !ok || name == sup.Team {
					continue
				}
				up, err := RoleInboxSubject(sup.Workspace, sup.Team, r.Name)
				if err != nil {
					return nil, err
				}
				supUser := sup.Team + "." + r.Name
				for _, nr := range named.Roles {
					down, err := RoleInboxSubject(named.Workspace, named.Team, nr.Name)
					if err != nil {
						return nil, err
					}
					add(supUser, down)
					add(named.Team+"."+nr.Name, up)
				}
			}
		}
	}
	for user := range out {
		slices.Sort(out[user])
	}
	return out, nil
}

// UnresolvedSupervises lists, as "<ws>/<team>.<role> -> <name>", each named
// team that is not applied in the supervisor's workspace, sorted.
func UnresolvedSupervises(applied []AppliedTeam) []string {
	have := make(map[[2]string]bool, len(applied))
	for _, t := range applied {
		have[[2]string{t.Workspace, t.Team}] = true
	}
	var out []string
	for _, sup := range applied {
		for _, r := range sup.Roles {
			for _, name := range r.Supervises {
				if !have[[2]string{sup.Workspace, name}] {
					out = append(out, sup.Workspace+"/"+sup.Team+"."+r.Name+" -> "+name)
				}
			}
		}
	}
	slices.Sort(out)
	return out
}

// checkToken refuses a name that is not one address token, so no wildcard,
// separator or space reaches a subject.
func checkToken(kind, s string) error {
	if !subjectToken.MatchString(s) {
		return fmt.Errorf("%s %q is not an addressable name", kind, s)
	}
	return nil
}

// appliedTeams projects the stored teams into the shape SupervisedGrants reads.
func appliedTeams(teams []api.Team) []AppliedTeam {
	out := make([]AppliedTeam, 0, len(teams))
	for _, t := range teams {
		at := AppliedTeam{Workspace: t.Workspace, Team: t.Name}
		for _, r := range t.Roles {
			at.Roles = append(at.Roles, AppliedRole{Name: r.Name, Supervises: r.Supervises})
		}
		out = append(out, at)
	}
	return out
}

// noteUnresolved emits bus.supervises-unresolved once for each supervises name
// that names no applied team in the supervisor's workspace, and forgets the
// ones that resolved since, so a name that later goes missing again is
// reported again. The caller holds m.mu.
func (m *Manager) noteUnresolved(teams []api.Team) {
	now := UnresolvedSupervises(appliedTeams(teams))
	next := make(map[string]bool, len(now))
	for _, key := range now {
		next[key] = true
		if m.unresolved[key] {
			continue
		}
		events.Emit(m.Events, events.Event{
			Kind: events.KindBusSupervisesUnresolved, Severity: events.SeverityWarning,
			Message: fmt.Sprintf("%s: supervises names a team that is not applied in the supervisor's workspace; it grants nothing until that team is applied", key),
		})
	}
	m.unresolved = next
}
