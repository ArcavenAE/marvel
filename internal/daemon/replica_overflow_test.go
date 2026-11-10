package daemon

import (
	"math"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// A declaration check that sums stored roles must not wrap: a scale-up that
// would push the declared total past the ceiling is refused even when the
// other roles already hold huge counts.
func TestAdmitDeclarationRefusesASumThatWouldWrap(t *testing.T) {
	d := newHandlerDaemon(t)
	tm := api.Team{
		Workspace: "ws", Name: "crew",
		Budget: api.Budget{MaxSessions: 6},
		Roles: []api.Role{
			{Name: "a", Replicas: math.MaxInt},
			{Name: "b", Replicas: 1},
		},
	}
	if resp := d.admitDeclaration(tm, "b", 5, 1); resp == nil {
		t.Fatal("a scale-up past the ceiling was admitted: the declared sum wrapped")
	}
}

// A stored count above the bound is reported at startup, like a negative one,
// and left as stored.
func TestReportInvalidReplicasNamesACountAboveTheBound(t *testing.T) {
	t.Parallel()
	store := api.NewStore()
	if err := store.CreateTeam(&api.Team{Workspace: "ws", Name: "crew", Roles: []api.Role{
		{Name: "big", Replicas: api.MaxScaleReplicas + 1},
		{Name: "ok", Replicas: 2},
	}}); err != nil {
		t.Fatalf("create team: %v", err)
	}
	ring := events.NewRing(16)
	reportInvalidReplicas(store, ring)
	got := ring.Snapshot(events.Filter{Kind: events.KindRoleReplicasInvalid}, 0)
	if len(got) != 1 || got[0].Role != "big" {
		t.Fatalf("reported %+v, want one %s event naming role big", got, events.KindRoleReplicasInvalid)
	}
	tm, err := store.GetTeam("ws/crew")
	if err != nil {
		t.Fatal(err)
	}
	if tm.Roles[0].Replicas != api.MaxScaleReplicas+1 {
		t.Errorf("stored count rewritten to %d, want it left as stored", tm.Roles[0].Replicas)
	}
}
