package runtime

import (
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// sandboxAdapter is an adapter that implements the capability, to stand for the
// day pi or any other harness gets an adapter of its own.
type sandboxAdapter struct {
	Generic
	name      string
	recommend bool
}

func (a *sandboxAdapter) Name() string            { return a.name }
func (a *sandboxAdapter) RecommendsSandbox() bool { return a.recommend }

// The two harnesses the design names (docs/design/workspace-sandbox-shape.md,
// section 4) recommend a container. Neither has an adapter of its own yet: pi has
// none and mini-swe-agent runs under generic, so the registry carries the
// declaration. A path or stray space does not hide the harness.
func TestRecommendedPlacementIsContainerForPiAndMiniSweAgent(t *testing.T) {
	reg := NewRegistry()
	for _, name := range []string{"pi", "mini-swe-agent", "/usr/local/bin/pi", " mini-swe-agent "} {
		if got := reg.RecommendedPlacement(name); got != api.PlacementContainer {
			t.Errorf("RecommendedPlacement(%q) = %q, want container", name, got)
		}
	}
}

// Every other harness has no recommendation, so the resolution order falls
// through to localhost for it.
func TestRecommendedPlacementIsEmptyForEveryOtherHarness(t *testing.T) {
	reg := NewRegistry()
	for _, name := range []string{"claude", "codex", "opencode", "forestage", "generic", "simulator", "goose", "pie", "pi-agent", "mini", ""} {
		if got := reg.RecommendedPlacement(name); got != "" {
			t.Errorf("RecommendedPlacement(%q) = %q, want no recommendation", name, got)
		}
	}
}

// An adapter that implements the capability speaks for its harness, and its
// answer beats the registry's table: once pi has an adapter, the adapter owns
// the declaration, in either direction.
func TestAnAdapterThatImplementsTheCapabilityBeatsTheTable(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&sandboxAdapter{name: "newharness", recommend: true})
	if got := reg.RecommendedPlacement("newharness"); got != api.PlacementContainer {
		t.Errorf("an adapter that recommends: got %q, want container", got)
	}
	reg.Register(&sandboxAdapter{name: "pi", recommend: false})
	if got := reg.RecommendedPlacement("pi"); got != "" {
		t.Errorf("an adapter named pi that does not recommend: got %q, want the table overridden", got)
	}
}

// The registry's answer feeds the resolution order from S1 unchanged: an
// explicit localhost still wins, and a harness with no recommendation lands on
// localhost.
func TestRecommendationFeedsTheResolutionOrder(t *testing.T) {
	reg := NewRegistry()
	for name, tc := range map[string]struct {
		harness  string
		explicit api.Placement
		want     api.Placement
	}{
		"pi with nothing set":                 {"pi", "", api.PlacementContainer},
		"pi with explicit localhost":          {"pi", api.PlacementLocalhost, api.PlacementLocalhost},
		"mini-swe-agent with nothing set":     {"mini-swe-agent", "", api.PlacementContainer},
		"claude with nothing set":             {"claude", "", api.PlacementLocalhost},
		"claude with explicit container":      {"claude", api.PlacementContainer, api.PlacementContainer},
		"an unknown harness with nothing set": {"whatever", "", api.PlacementLocalhost},
	} {
		got, err := api.ResolvePlacement(tc.explicit, reg.RecommendedPlacement(tc.harness))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: resolved %q, want %q", name, got, tc.want)
		}
	}
}

// The table never answers for an adapter that exists and says no: no registered
// adapter of this repo recommends a sandbox today, so none may resolve to one.
func TestNoShippedAdapterRecommendsASandboxYet(t *testing.T) {
	reg := NewRegistry()
	for name, a := range reg.adapters {
		if r, ok := a.(SandboxRecommender); ok && r.RecommendsSandbox() {
			t.Errorf("adapter %q recommends a sandbox; the design says only pi and mini-swe-agent do", name)
		}
	}
}
