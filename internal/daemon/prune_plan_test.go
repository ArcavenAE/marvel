package daemon

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/events"
)

const pruneSrc = "host-a:/srv/manifests/main.toml"

const pruneManifest = `
[workspace]
name = "ws"

[[team]]
name = "kept"

  [[team.role]]
  name = "worker"
  replicas = 0

    [team.role.runtime]
    command = "bash"
`

func pruneDaemon(t *testing.T) *Daemon {
	t.Helper()
	s := api.NewStore()
	at := time.Date(2026, 10, 27, 18, 0, 0, 0, time.UTC)
	if err := s.CreateWorkspace(&api.Workspace{Name: "ws", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"kept", "gone"} {
		team := &api.Team{
			Name: name, Workspace: "ws", Roles: []api.Role{{Name: "worker"}},
			Claims: &api.TeamClaims{Sources: map[string]time.Time{pruneSrc: at}},
		}
		if err := s.CreateTeam(team); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CreateSession(&api.Session{Name: "gone-worker-g1-0", Workspace: "ws", Team: "gone", Role: "worker", Generation: 1, State: api.SessionFailed}); err != nil {
		t.Fatal(err)
	}
	return &Daemon{store: s, events: events.NewRing(64)}
}

func prunePlanRequest(t *testing.T, d *Daemon, source string) daemonrpc.Response {
	t.Helper()
	p := map[string]any{"manifest_data": []byte(pruneManifest)}
	if source != "" {
		p["manifest_source"] = source
	}
	params, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return d.dispatchAs(daemonrpc.Request{Method: "prune_plan", Params: params}, caller{scope: ScopeAdmin, local: true})
}

type storeView struct {
	Workspaces []api.Workspace
	Teams      []api.Team
	Sessions   []api.Session
}

func viewOf(s *api.Store) storeView {
	return storeView{s.ListWorkspaces(), s.ListTeams(), s.ListSessions()}
}

// prune_plan is read-only: it answers with the plan and writes nothing to the
// store and emits no event. A daemon with no tmux driver also shows it never
// reaches tmux, since any call there would panic.
func TestPrunePlanWritesNothing(t *testing.T) {
	d := pruneDaemon(t)
	before := viewOf(d.store)

	resp := prunePlanRequest(t, d, pruneSrc)
	if resp.Error != "" {
		t.Fatalf("prune_plan: %s", resp.Error)
	}
	var plan api.PrunePlan
	if err := json.Unmarshal(resp.Result, &plan); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	if len(plan.WouldDelete) != 1 || plan.WouldDelete[0].Team != "ws/gone" {
		t.Errorf("would delete = %+v, want ws/gone", plan.WouldDelete)
	}
	if after := viewOf(d.store); !reflect.DeepEqual(before, after) {
		t.Errorf("prune_plan changed the store:\nbefore %+v\nafter  %+v", before, after)
	}
	if n := d.events.Len(); n != 0 {
		t.Errorf("prune_plan emitted %d event(s): %+v", n, d.events.Snapshot(events.Filter{}, n))
	}
}

// prune_plan refuses an empty manifest source: nothing can be pruned without
// one (the step 2 ruling, Q7).
func TestPrunePlanRefusesAnEmptySource(t *testing.T) {
	d := pruneDaemon(t)
	resp := prunePlanRequest(t, d, "")
	if resp.Error == "" || !strings.Contains(resp.Error, "manifest source") {
		t.Errorf("prune_plan with no source = %q, %s; want a refusal naming the manifest source", resp.Error, resp.Result)
	}
}
