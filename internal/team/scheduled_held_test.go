package team

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// A stored scheduled role that is refused now loads marked held and never
// launches, even when a firing is due; its siblings plan as before
// (aae-orc-iyun3 part c).

func heldWS() string { return "hws" }

func heldSchedRole(name, held string) api.Role {
	return api.Role{
		Name: name, Replicas: 1, Held: held,
		Schedule: &api.SchedulePolicy{Cron: "0 6 * * *", Timezone: "Etc/UTC"},
		Runtime:  api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}, Mode: api.RuntimeModeHeadless, Prompt: "go"},
	}
}

func TestPlanRoleNeverSpawnsAHeldScheduledRole(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	reason := "refused now: this launch skips permission checks"
	roles := []api.Role{
		heldSchedRole("held", reason),
		heldSchedRole("ok", ""),
		{Name: "seat", Replicas: 1, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
	}
	createTeamFixture(t, store, heldWS(), "ops", roles)
	// A firing is due for both scheduled roles, so only the mark can stop one.
	for _, name := range []string{"held", "ok"} {
		key := heldWS() + "/ops/" + name
		if _, err := store.UpdateScheduleStatus(key, func(st *api.ScheduleStatus) bool {
			st.Key, st.Firing, st.Since = key, "f1", time.Now().UTC()
			return true
		}); err != nil {
			t.Fatal(err)
		}
	}
	team, err := store.GetTeam(heldWS() + "/ops")
	if err != nil {
		t.Fatal(err)
	}
	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()

	plan := ctrl.planRole(&team, &team.Roles[0], team.Generation)
	if plan.Action != RoleHold || plan.Hold != HoldScheduleRefused || plan.Spawn != 0 {
		t.Errorf("held role plan = %+v, want RoleHold/HoldScheduleRefused with no spawn", plan)
	}
	if !strings.Contains(plan.HoldDetail, reason) {
		t.Errorf("hold detail = %q, want the stored reason", plan.HoldDetail)
	}
	for _, i := range []int{1, 2} {
		p := ctrl.planRole(&team, &team.Roles[i], team.Generation)
		if p.Hold == HoldScheduleRefused || (p.Action != RoleSpawn) {
			t.Errorf("sibling %s plan = %+v, want it to spawn as before", team.Roles[i].Name, p)
		}
	}
}
