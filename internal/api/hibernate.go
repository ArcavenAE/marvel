package api

import (
	"slices"
	"time"
)

// HibernateState is where a team's hibernate mark stands.
type HibernateState string

const (
	// HibernateHibernated is the mark from a hibernate's first sign-off request
	// until a wake. While it stands nothing spawns for the covered roles.
	HibernateHibernated HibernateState = "hibernated"
	// HibernateWaking is the mark a wake leaves in place while it brings the
	// team back: supervisor roles may spawn, and the other roles once a
	// supervisor is up.
	HibernateWaking HibernateState = "waking"
)

// BackendNone is the word a hibernate selector uses for a role with no backend
// label. It is reserved: apply refuses it as a declared label, so it can never
// mean two things.
const BackendNone = "none"

// HibernateModeHandoff is the one mode phase 2 has: every seat comes back as a
// fresh successor reading its handoff.
const HibernateModeHandoff = "handoff"

// HibernateMark is team intent, stored with the team: when a hibernate began,
// who asked, through what, and in which mode (docs/design/fleet-hibernate.md
// section 4, step 5). Roles names the roles the mark covers; empty means every
// role, which is a whole-team hibernate.
type HibernateMark struct {
	State  HibernateState
	Since  time.Time
	Actor  string
	Source string
	Mode   string
	Roles  []string `json:",omitempty"`
}

// Covers reports whether the mark covers role.
func (m *HibernateMark) Covers(role string) bool {
	if m == nil {
		return false
	}
	return len(m.Roles) == 0 || slices.Contains(m.Roles, role)
}

// Clone returns a copy that shares nothing with m.
func (m *HibernateMark) Clone() *HibernateMark {
	if m == nil {
		return nil
	}
	c := *m
	c.Roles = slices.Clone(m.Roles)
	return &c
}

// Hibernating reports whether the team carries a hibernate mark in any state.
func (t *Team) Hibernating() bool { return t.Hibernate != nil }

// HibernateCovers reports whether the team's mark covers role.
func (t *Team) HibernateCovers(role string) bool { return t.Hibernate.Covers(role) }

// HibernateRolesAfter returns the roles a mark covers once a hibernate of roles
// is added to it: a whole-team hibernate (empty roles) empties the list, and a
// subset adds its roles to a list that is not already everything. The result is
// sorted and has no duplicates.
func HibernateRolesAfter(prev *HibernateMark, roles []string) []string {
	if len(roles) == 0 || (prev != nil && len(prev.Roles) == 0) {
		return nil
	}
	var out []string
	if prev != nil {
		out = slices.Clone(prev.Roles)
	}
	out = append(out, roles...)
	slices.Sort(out)
	return slices.Compact(out)
}

// BackendLabel is the label a hibernate selector reads for role: its declared
// backend, or BackendNone when it declares none.
func BackendLabel(r Role) string {
	if r.Runtime.Backend == "" {
		return BackendNone
	}
	return r.Runtime.Backend
}

// SelectRoles returns the names of the roles a backend selection covers, in
// the order given (docs/design/fleet-hibernate.md section 7). include selects
// only the roles whose label it lists, so an unlabeled role is selected only
// when include lists "none". except selects every role whose label it does not
// list, so an unlabeled role is selected unless except lists "none". Both lists
// empty selects every role; given together, a role must satisfy both.
func SelectRoles(roles []Role, include, except []string) []string {
	var out []string
	for _, r := range roles {
		label := BackendLabel(r)
		if len(include) > 0 && !slices.Contains(include, label) {
			continue
		}
		if slices.Contains(except, label) {
			continue
		}
		out = append(out, r.Name)
	}
	return out
}

// IsSupervisorRole reports whether r is a supervisor role: its global_role is
// supervisor, or it declares none and is named supervisor.
func IsSupervisorRole(r Role) bool {
	return r.GlobalRole == GlobalRoleSupervisor || (r.GlobalRole == "" && r.Name == "supervisor")
}
