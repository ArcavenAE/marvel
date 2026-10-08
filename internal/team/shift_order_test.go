package team

import (
	"slices"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// shiftRoles builds roles from names; a name in supers also declares
// global_role = "supervisor".
func shiftRoles(supers []string, names ...string) []api.Role {
	roles := make([]api.Role, 0, len(names))
	for _, n := range names {
		r := api.Role{Name: n}
		if slices.Contains(supers, n) {
			r.GlobalRole = api.GlobalRoleSupervisor
		}
		roles = append(roles, r)
	}
	return roles
}

// A role that declares global_role supervisor shifts after the workers it
// watches, whatever it is named (#521).
func TestShiftOrderPutsADeclaredSupervisorLast(t *testing.T) {
	roles := shiftRoles([]string{"arcaven-supervisor"}, "arcaven-supervisor", "worker-a", "worker-b")
	got := shiftOrder(roles)
	want := []string{"worker-a", "worker-b", "arcaven-supervisor"}
	if !slices.Equal(got, want) {
		t.Errorf("shiftOrder = %v, want %v", got, want)
	}
}

// The literal name stays a fallback: a role named supervisor with no
// global_role still shifts last, as it did before the field existed.
func TestShiftOrderKeepsTheLiteralNameFallback(t *testing.T) {
	roles := shiftRoles(nil, "supervisor", "worker-a", "worker-b")
	got := shiftOrder(roles)
	want := []string{"worker-a", "worker-b", "supervisor"}
	if !slices.Equal(got, want) {
		t.Errorf("shiftOrder = %v, want %v", got, want)
	}
}

// Two supervisor-type roles keep their roster order between themselves,
// after every other role, and the workers keep theirs.
func TestShiftOrderKeepsRosterOrderAmongSupervisors(t *testing.T) {
	roles := shiftRoles([]string{"watcher"}, "watcher", "worker-a", "supervisor", "worker-b")
	got := shiftOrder(roles)
	want := []string{"worker-a", "worker-b", "watcher", "supervisor"}
	if !slices.Equal(got, want) {
		t.Errorf("shiftOrder = %v, want %v", got, want)
	}

	roles = shiftRoles([]string{"watcher"}, "supervisor", "worker-a", "watcher", "worker-b")
	got = shiftOrder(roles)
	want = []string{"worker-a", "worker-b", "supervisor", "watcher"}
	if !slices.Equal(got, want) {
		t.Errorf("shiftOrder (supervisor first in the roster) = %v, want %v", got, want)
	}
}

// With no supervisor-type role the roster order is untouched.
func TestShiftOrderLeavesWorkersInRosterOrder(t *testing.T) {
	got := shiftOrder(shiftRoles(nil, "c", "a", "b"))
	want := []string{"c", "a", "b"}
	if !slices.Equal(got, want) {
		t.Errorf("shiftOrder = %v, want %v", got, want)
	}
}

// The name fallback applies only where global_role is unset. A role named
// supervisor that declares global_role "none" opted out, as
// config.ResolvedGlobalRole reads it, so it keeps its roster place.
func TestShiftOrderHonorsAnExplicitNone(t *testing.T) {
	roles := shiftRoles(nil, "w1", "supervisor", "w2")
	roles[1].GlobalRole = api.GlobalRoleNone
	got := shiftOrder(roles)
	want := []string{"w1", "supervisor", "w2"}
	if !slices.Equal(got, want) {
		t.Errorf("shiftOrder = %v, want %v", got, want)
	}
}
