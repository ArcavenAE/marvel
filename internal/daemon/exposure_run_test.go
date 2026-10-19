package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// TestHandleRunRefusesAWriterWhoseSlotHasRead: an ad-hoc `marvel run` creates
// its session without a plan, so a controller-only gate leaves a hole
// (marvel#945). The real handler must refuse and leave no row.
func TestHandleRunRefusesAWriterWhoseSlotHasRead(t *testing.T) {
	d := newHandlerDaemon(t)
	if err := d.store.CreateWorkspace(&api.Workspace{Name: "xrun"}); err != nil {
		t.Fatal(err)
	}
	if err := d.store.CreateTeam(&api.Team{Name: "crew", Workspace: "xrun", Roles: []api.Role{
		{Name: "writer", Replicas: 0, Writes: []string{"parse"}},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.RecordExposure(api.ExposureRecord{
		Workspace: "xrun", Team: "crew", Role: "writer", Function: "parse",
		Caster: "supervisor", StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	resp := d.handleRun(mustMarshal(t, runParams{
		Workspace: "xrun", Team: "crew", Role: "writer", Name: "run-x",
		RuntimeCommand: "sleep", RuntimeArgs: []string{"300"},
	}))
	if resp.Error == "" || !strings.Contains(resp.Error, "exposure") {
		t.Fatalf("run error = %q, want a refusal that names the exposure", resp.Error)
	}
	if rows := d.store.ListSessionsByTeam("xrun", "crew"); len(rows) != 0 {
		t.Errorf("a refused run left %d session rows", len(rows))
	}

	// The control: a role of the same team that declares no writes still runs.
	if err := d.store.UpdateTeam("xrun/crew", func(live *api.Team) error {
		live.Roles = append(live.Roles, api.Role{Name: "reader", Replicas: 0})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	resp = d.handleRun(mustMarshal(t, runParams{
		Workspace: "xrun", Team: "crew", Role: "reader", Name: "run-y",
		RuntimeCommand: "sleep", RuntimeArgs: []string{"300"},
	}))
	if resp.Error != "" {
		t.Errorf("a run for a role with no writes was refused: %s", resp.Error)
	}
}
