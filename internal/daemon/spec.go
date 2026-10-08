package daemon

import (
	"github.com/arcavenae/marvel/internal/api"
)

// stampSpec fills Spec and SpecDiff on the copies a read serves: whether each
// session's stored runtime still equals its role's current one
// (docs/design/drift-view.md). The answer is derived and never stored, since
// a stored one would go stale, which is the thing this view shows. A session
// with no role in the team spec is left empty, and so is a session that was
// not spawned from its role: marvel run stores a session under whatever team
// and role it names with no generation, and it never takes the role's spec at a
// respawn, so "behind" would promise a change that does not come.
func (d *Daemon) stampSpec(sessions []api.Session) {
	teams := map[string]api.Team{}
	for i := range sessions {
		s := &sessions[i]
		if s.Role == "" || s.Generation == 0 {
			continue
		}
		key := s.Workspace + "/" + s.Team
		t, ok := teams[key]
		if !ok {
			var err error
			if t, err = d.store.GetTeam(key); err != nil {
				continue
			}
			teams[key] = t
		}
		for j := range t.Roles {
			if t.Roles[j].Name != s.Role {
				continue
			}
			s.SpecDiff = api.RuntimeDrift(s.Runtime, t.Roles[j].Runtime)
			s.Spec = api.SpecCurrent
			if len(s.SpecDiff) > 0 {
				s.Spec = api.SpecBehind
			}
			break
		}
	}
}

// behindCount is how many stored sessions of the named teams are behind their
// role. A finished headless run counts: it keeps its slot (ADR-010).
func (d *Daemon) behindCount(workspace string, teams []string) int {
	var all []api.Session
	for _, name := range teams {
		all = append(all, d.store.ListSessionsByTeam(workspace, name)...)
	}
	d.stampSpec(all)
	n := 0
	for i := range all {
		if all[i].Spec == api.SpecBehind {
			n++
		}
	}
	return n
}
