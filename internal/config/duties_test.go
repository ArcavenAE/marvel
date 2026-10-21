package config

import (
	"slices"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// ResolvedDuties is the one answer to "which duties does this role's seat
// carry" (docs/design/seat-readiness.md section 3). Every seat marvel spawns
// carries first-turn and ack; a role's declared duties add to them. That
// "every supervised seat" means every seat marvel spawns is this package's
// reading; the design does not define the word.

func TestResolvedDutiesAlwaysCarriesFirstTurnAndAck(t *testing.T) {
	t.Parallel()
	want := []string{api.DutyFirstTurn, api.DutyAck}
	for _, role := range []api.Role{
		{Name: "worker"},
		{Name: "supervisor", GlobalRole: api.GlobalRoleSupervisor},
		{Name: "probe", GlobalRole: api.GlobalRoleNone},
	} {
		if got := ResolvedDuties(role); !slices.Equal(got, want) {
			t.Errorf("role %s: ResolvedDuties = %v, want %v", role.Name, got, want)
		}
	}
}

func TestResolvedDutiesAddsDeclaredDutiesAfterTheBase(t *testing.T) {
	t.Parallel()
	role := api.Role{Name: "reviewer", Duties: []string{api.DutyGHRead, api.DutyGHComment}}
	want := []string{api.DutyFirstTurn, api.DutyAck, api.DutyGHRead, api.DutyGHComment}
	if got := ResolvedDuties(role); !slices.Equal(got, want) {
		t.Errorf("ResolvedDuties = %v, want %v", got, want)
	}
}

// A role that also declares a base duty gets it once.
func TestResolvedDutiesListsADutyOnce(t *testing.T) {
	t.Parallel()
	role := api.Role{Name: "envoy", Duties: []string{api.DutyAck, api.DutyGHPost, api.DutyFirstTurn}}
	want := []string{api.DutyFirstTurn, api.DutyAck, api.DutyGHPost}
	if got := ResolvedDuties(role); !slices.Equal(got, want) {
		t.Errorf("ResolvedDuties = %v, want %v", got, want)
	}
}

// Parse refuses a name outside the library, but a role can be built in code or
// read from an older store. The resolver answers only with library names, so
// nothing downstream is asked to check a duty no check serves.
func TestResolvedDutiesDropsANameOutsideTheLibrary(t *testing.T) {
	t.Parallel()
	role := api.Role{Name: "worker", Duties: []string{"gh.delete", api.DutyJiraRead}}
	want := []string{api.DutyFirstTurn, api.DutyAck, api.DutyJiraRead}
	if got := ResolvedDuties(role); !slices.Equal(got, want) {
		t.Errorf("ResolvedDuties = %v, want %v", got, want)
	}
}

// The answer is the caller's to keep: changing it does not change the role.
func TestResolvedDutiesDoesNotAliasTheRole(t *testing.T) {
	t.Parallel()
	role := api.Role{Name: "reviewer", Duties: []string{api.DutyGHRead}}
	got := ResolvedDuties(role)
	if len(got) == 0 {
		t.Fatal("ResolvedDuties is empty")
	}
	got[len(got)-1] = api.DutyJiraWrite
	if role.Duties[0] != api.DutyGHRead {
		t.Errorf("changing the answer changed the role: %v", role.Duties)
	}
}
