package runtime

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// simulatorContext mirrors the demo manifests: image "simulator", a
// relative bin/simulator command, a --tick arg, and a lua script on the
// supervisor role.
func simulatorContext() *LaunchContext {
	return &LaunchContext{
		Session: &api.Session{
			Name:      "squad-agent-g1-0",
			Workspace: "demo",
			Team:      "squad",
			Role:      "agent",
			Runtime: api.Runtime{
				Name:    "simulator",
				Command: "bin/simulator",
				Args:    []string{"--tick", "3000"},
				Script:  "scripts/chaos.lua",
			},
		},
		Role: &api.Role{
			Name:     "agent",
			Replicas: 3,
			Runtime: api.Runtime{
				Name:    "simulator",
				Command: "bin/simulator",
			},
		},
		Team:       &api.Team{Name: "squad", Workspace: "demo"},
		Workspace:  &api.Workspace{Name: "demo"},
		SocketPath: "/tmp/marvel.sock",
	}
}

// TestRegistryResolveSimulator: image "simulator" must reach the dedicated
// adapter, not the generic fallback. This is the whole point of the fix —
// the generic adapter does not inject the heartbeat flags.
func TestRegistryResolveSimulator(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if a := r.Resolve("simulator"); a.Name() != "simulator" {
		t.Fatalf(`Resolve("simulator") = %q, want "simulator"`, a.Name())
	}
}

// TestSimulatorPrepareInjectsHeartbeatFlags asserts the simulator gets the
// exact flags its heartbeat path reads: --socket (enables the RPC),
// --name + --workspace (the "<workspace>/<name>" session key the daemon
// matches), plus --team/--role and the lua --script. Without these the
// simulator never heartbeats and restart-loops (ArcavenAE/marvel#87).
func TestSimulatorPrepareInjectsHeartbeatFlags(t *testing.T) {
	t.Parallel()
	s := &Simulator{}
	ctx := simulatorContext()

	result, err := s.Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if !strings.HasPrefix(result.Command, "bin/simulator") {
		t.Errorf("command should start with binary, got: %s", result.Command)
	}
	for _, want := range []string{
		"--socket /tmp/marvel.sock",
		"--name squad-agent-g1-0",
		"--workspace demo",
		"--team squad",
		"--role agent",
		"--script scripts/chaos.lua",
		"--tick 3000",
	} {
		if !strings.Contains(result.Command, want) {
			t.Errorf("command missing %q, got: %s", want, result.Command)
		}
	}
}

// TestSimulatorPrepareNoSocket: with no daemon socket there is nothing to
// heartbeat to, so --socket must be omitted (the simulator only wires
// OnHeartbeat when --socket is non-empty).
func TestSimulatorPrepareNoSocket(t *testing.T) {
	t.Parallel()
	s := &Simulator{}
	ctx := simulatorContext()
	ctx.SocketPath = ""

	result, err := s.Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if strings.Contains(result.Command, "--socket") {
		t.Errorf("command should not contain --socket when SocketPath is empty, got: %s", result.Command)
	}
}

// TestSimulatorProjectionUnsupported: the simulator has no Claude Code
// settings surface, so it reports no projection target.
func TestSimulatorProjectionUnsupported(t *testing.T) {
	t.Parallel()
	s := &Simulator{}
	if got := s.ProjectionFor(simulatorContext(), "/tmp/x"); got.Supported {
		t.Errorf("simulator should not support projection, got %+v", got)
	}
}
