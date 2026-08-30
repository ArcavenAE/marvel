package daemon

import (
	"encoding/json"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/team"
)

// The plan RPC is the read-only preview surface (aae-orc-nrk1): it must
// report what the next reconcile tick would spawn WITHOUT spawning it. This
// test declares a role with a deficit, calls the handler directly (never
// Start, so the reconcile loop never runs), and asserts both that the plan
// names the deficit and that not a single session was created.
func TestHandlePlanReadOnly(t *testing.T) {
	skipIfNoTmux(t)

	d, err := New()
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	t.Cleanup(func() { d.Stop() })

	if err := d.store.CreateTeam(&api.Team{
		Name:      "squad",
		Workspace: "test",
		Roles:     []api.Role{{Name: "worker", Replicas: 2}},
	}); err != nil {
		t.Fatalf("create team: %v", err)
	}

	resp := d.handlePlan()
	if resp.Error != "" {
		t.Fatalf("plan error: %s", resp.Error)
	}

	var result struct {
		Plans []team.RolePlan `json:"plans"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("parse plan result: %v", err)
	}

	if len(result.Plans) != 1 {
		t.Fatalf("got %d plans, want 1: %+v", len(result.Plans), result.Plans)
	}
	p := result.Plans[0]
	if p.Workspace != "test" || p.Team != "squad" || p.Role != "worker" {
		t.Errorf("plan identity = %s/%s/%s, want test/squad/worker", p.Workspace, p.Team, p.Role)
	}
	if p.Desired != 2 {
		t.Errorf("Desired = %d, want 2", p.Desired)
	}
	if p.Actual != 0 {
		t.Errorf("Actual = %d, want 0", p.Actual)
	}
	if p.Action != team.RoleSpawn {
		t.Errorf("Action = %q, want %q", p.Action, team.RoleSpawn)
	}
	if p.Spawn != 2 {
		t.Errorf("Spawn = %d, want 2", p.Spawn)
	}

	// The point of the surface: computing the plan spawns nothing.
	if got := len(d.store.ListSessions()); got != 0 {
		t.Errorf("plan created %d sessions; the preview must spawn nothing", got)
	}
}

// A team mid-shift is out of scope for the steady-state preview (see the
// PlanConvergence doc comment). The handler must simply omit it rather than
// error.
func TestHandlePlanEmpty(t *testing.T) {
	skipIfNoTmux(t)

	d, err := New()
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	t.Cleanup(func() { d.Stop() })

	resp := d.handlePlan()
	if resp.Error != "" {
		t.Fatalf("plan error: %s", resp.Error)
	}
	var result struct {
		Plans []team.RolePlan `json:"plans"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("parse plan result: %v", err)
	}
	if len(result.Plans) != 0 {
		t.Errorf("got %d plans with no teams, want 0", len(result.Plans))
	}
}
