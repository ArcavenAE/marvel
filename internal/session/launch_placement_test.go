package session

import (
	"fmt"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/runtime"
)

// placementSpy records the LaunchContext its Prepare was handed.
type placementSpy struct{ got *runtime.LaunchContext }

func (*placementSpy) Name() string { return "placementspy" }

func (s *placementSpy) Prepare(ctx *runtime.LaunchContext) (*runtime.LaunchResult, error) {
	s.got = ctx
	return &runtime.LaunchResult{Command: "placementspy"}, nil
}

func (*placementSpy) ProjectionFor(*runtime.LaunchContext, string) runtime.ProjectionTarget {
	return runtime.ProjectionTarget{}
}

const placementManifestFmt = `
[workspace]
name = "acme"
%s

[[team]]
name = "squad"

  [[team.role]]
  name = "reviewer"
  replicas = 1

    [team.role.runtime]
    image = "placementspy"
    command = "placementspy"
`

// planLaunch hands the adapter the placement the manager resolved, and no
// sandbox profile path, since nothing in the manager launches under one yet.
func TestPlanLaunchHandsTheAdapterTheResolvedPlacement(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		line string
		want api.Placement
	}{
		{"explicit container", `placement = "container"`, api.PlacementContainer},
		{"explicit localhost", `placement = "localhost"`, api.PlacementLocalhost},
		{"unset resolves to the default", ``, api.PlacementLocalhost},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			spy := &placementSpy{}
			mgr := &Manager{
				store:         api.NewStore(),
				adapters:      runtime.NewRegistry(),
				ProjectionDir: t.TempDir(),
				Events:        events.NewRing(16),
			}
			mgr.adapters.Register(spy)
			m, err := api.ParseManifestBytes([]byte(sprintfManifest(tc.line)))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if err := m.Apply(mgr.store); err != nil {
				t.Fatalf("apply: %v", err)
			}
			mgr.planLaunch(sessionFor("reviewer", "placementspy"))
			if spy.got == nil {
				t.Fatal("Prepare was not called")
			}
			if spy.got.Placement != tc.want {
				t.Errorf("Placement = %q, want %q", spy.got.Placement, tc.want)
			}
			if spy.got.SandboxProfile != "" {
				t.Errorf("SandboxProfile = %q, want empty: nothing launches under a profile yet", spy.got.SandboxProfile)
			}
		})
	}
}

func sprintfManifest(line string) string { return fmt.Sprintf(placementManifestFmt, line) }
