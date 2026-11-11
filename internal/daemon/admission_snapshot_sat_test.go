package daemon

import (
	"math"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// TestAdmissionSnapshotSaturatesDeclaredSessions pins the declared-session
// sum in AdmissionSnapshot. Two roles that each declare the largest int would
// wrap a plain sum to a negative count, which every budget comparison would
// then admit; api.AddSat holds it at the top instead. It builds the daemon
// from a store alone, so nothing starts.
func TestAdmissionSnapshotSaturatesDeclaredSessions(t *testing.T) {
	d := &Daemon{store: api.NewStore()}
	team := api.Team{
		Workspace: "ws",
		Name:      "crew",
		Roles: []api.Role{
			{Name: "a", Replicas: math.MaxInt},
			{Name: "b", Replicas: math.MaxInt},
		},
	}

	snap := d.AdmissionSnapshot(team)
	if snap.DeclaredSessions != math.MaxInt {
		t.Fatalf("DeclaredSessions = %d, want %d: the sum must saturate, not wrap", snap.DeclaredSessions, math.MaxInt)
	}
}
