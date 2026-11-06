package daemon

import (
	"sort"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/register"
)

// registerRows is the seat register: one row for each live session, built by
// register.Build and ordered by seat key. A session whose team or role no
// longer resolves still gets a row, with the duties every seat carries. The
// rows describe seats that exist, so the synthetic rows get sessions adds for a
// role that is down in its backoff window have no row here. Standing cells are
// not filled yet; Build leaves them unmeasured.
func (d *Daemon) registerRows() []register.Row {
	live := d.store.ListSessions()
	rows := make([]register.Row, 0, len(live))
	for i := range live {
		rows = append(rows, register.Build(register.Input{
			Seat:   live[i].Key(),
			Duties: config.ResolvedDuties(d.roleOfSession(live[i])),
		}))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Seat < rows[j].Seat })
	return rows
}

// roleOfSession is the role a session belongs to, or the zero role when its
// team or role is no longer in the store.
func (d *Daemon) roleOfSession(s api.Session) api.Role {
	t, err := d.store.GetTeam(s.Workspace + "/" + s.Team)
	if err != nil {
		return api.Role{}
	}
	for _, r := range t.Roles {
		if r.Name == s.Role {
			return r
		}
	}
	return api.Role{}
}
