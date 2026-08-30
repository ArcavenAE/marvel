package main

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/team"
)

// With no plans — no teams declared, or every team mid-shift — the preview
// says so in words rather than printing a bare header, so an operator does
// not read an empty table as "nothing to do here" when it means "nothing
// to plan against".
func TestRenderPlanTableEmpty(t *testing.T) {
	out := renderPlanTable(nil)
	if !strings.Contains(out, "no teams to plan") {
		t.Errorf("empty render = %q, want a 'no teams to plan' message", out)
	}
}

// A single role with a deficit renders each field of the plan in its own
// column: desired, the alive count it would adopt toward, the verb, and the
// spawn count.
func TestRenderPlanTableSpawn(t *testing.T) {
	table := renderPlanTable([]team.RolePlan{{
		Workspace: "ws", Team: "api", Role: "dev",
		Generation: 0, Desired: 2, Actual: 1,
		Action: team.RoleSpawn, Spawn: 1,
	}})
	if got := column(t, table, "WORKSPACE"); got != "ws" {
		t.Errorf("WORKSPACE = %q, want ws", got)
	}
	if got := column(t, table, "ROLE"); got != "dev" {
		t.Errorf("ROLE = %q, want dev", got)
	}
	if got := column(t, table, "DESIRED"); got != "2" {
		t.Errorf("DESIRED = %q, want 2", got)
	}
	if got := column(t, table, "ACTUAL"); got != "1" {
		t.Errorf("ACTUAL = %q, want 1", got)
	}
	if got := column(t, table, "ACTION"); got != "spawn" {
		t.Errorf("ACTION = %q, want spawn", got)
	}
	if got := column(t, table, "SPAWN"); got != "1" {
		t.Errorf("SPAWN = %q, want 1", got)
	}
	if got := column(t, table, "HOLD"); got != "-" {
		t.Errorf("HOLD = %q, want - for an unheld spawn", got)
	}
}

// A scale-down reports the number of sessions it would delete, and a steady
// role reports zero of everything.
func TestRenderPlanTableScaleDownAndSteady(t *testing.T) {
	scaleDown := renderPlanTable([]team.RolePlan{{
		Workspace: "ws", Team: "api", Role: "dev",
		Desired: 1, Actual: 3, Action: team.RoleScaleDown,
		Delete: []api.Session{{Name: "a"}, {Name: "b"}},
	}})
	if got := column(t, scaleDown, "SCALE-DOWN"); got != "2" {
		t.Errorf("SCALE-DOWN = %q, want 2", got)
	}
	if got := column(t, scaleDown, "ACTION"); got != "scale_down" {
		t.Errorf("ACTION = %q, want scale_down", got)
	}

	steady := renderPlanTable([]team.RolePlan{{
		Workspace: "ws", Team: "api", Role: "dev",
		Desired: 1, Actual: 1, Action: team.RoleSteady,
	}})
	if got := column(t, steady, "SPAWN"); got != "0" {
		t.Errorf("SPAWN = %q, want 0 for steady", got)
	}
	if got := column(t, steady, "ACTION"); got != "steady" {
		t.Errorf("ACTION = %q, want steady", got)
	}
}

// A held role carries its human-readable reason in the HOLD column — the
// backoff window or the admission refusal — so the preview explains why a
// deficit is not being repaired this tick.
func TestRenderPlanTableHoldDetail(t *testing.T) {
	table := renderPlanTable([]team.RolePlan{{
		Workspace: "ws", Team: "db", Role: "main",
		Desired: 1, Actual: 0, Action: team.RoleHold,
		Hold: team.HoldBackoff, HoldDetail: "crash-loop backoff until 2026-01-01T00:00:00Z",
	}})
	if !strings.Contains(table, "crash-loop backoff until 2026-01-01T00:00:00Z") {
		t.Errorf("hold detail missing from render:\n%s", table)
	}
	if got := column(t, table, "ACTION"); got != "hold" {
		t.Errorf("ACTION = %q, want hold", got)
	}
}

// The rows are sorted workspace, then team, then role — deterministic
// regardless of the order the daemon returned them — and a summary footer
// counts the verbs so the whole preview reads at a glance.
func TestRenderPlanTableSortedWithSummary(t *testing.T) {
	table := renderPlanTable([]team.RolePlan{
		{Workspace: "ws", Team: "b", Role: "z", Desired: 1, Actual: 1, Action: team.RoleSteady},
		{Workspace: "ws", Team: "a", Role: "x", Desired: 2, Actual: 0, Action: team.RoleSpawn, Spawn: 2},
		{Workspace: "ws", Team: "a", Role: "y", Desired: 0, Actual: 1, Action: team.RoleScaleDown, Delete: []api.Session{{Name: "s"}}},
	})
	lines := strings.Split(strings.TrimRight(table, "\n"), "\n")
	// lines[0] is the header; rows follow in sorted order.
	order := [][2]string{{"a", "x"}, {"a", "y"}, {"b", "z"}}
	for i, want := range order {
		cells := splitColumns(lines[1+i])
		// TEAM is column index 1, ROLE index 2.
		if cells[1] != want[0] || cells[2] != want[1] {
			t.Errorf("row %d = team %q role %q, want team %q role %q", i, cells[1], cells[2], want[0], want[1])
		}
	}
	if !strings.Contains(table, "1 spawn") || !strings.Contains(table, "1 scale-down") || !strings.Contains(table, "1 steady") {
		t.Errorf("summary footer missing expected counts:\n%s", table)
	}
}

// filterPlans narrows a preview to one workspace/team key, the optional
// argument to `marvel plan`. A bare token with no slash matches either the
// workspace or the team, so `marvel plan api` finds team api in any
// workspace.
func TestFilterPlans(t *testing.T) {
	plans := []team.RolePlan{
		{Workspace: "ws1", Team: "api", Role: "dev"},
		{Workspace: "ws1", Team: "db", Role: "main"},
		{Workspace: "ws2", Team: "api", Role: "dev"},
	}

	byKey := filterPlans(plans, "ws1/api")
	if len(byKey) != 1 || byKey[0].Workspace != "ws1" || byKey[0].Team != "api" {
		t.Errorf("filter ws1/api = %+v, want the single ws1/api row", byKey)
	}

	byTeam := filterPlans(plans, "api")
	if len(byTeam) != 2 {
		t.Errorf("filter api = %d rows, want 2 (api in both workspaces)", len(byTeam))
	}

	none := filterPlans(plans, "ws1/nope")
	if len(none) != 0 {
		t.Errorf("filter ws1/nope = %d rows, want 0", len(none))
	}
}
