package usage

import (
	"testing"

	"github.com/arcavenae/marvel/internal/runtime"
	"github.com/arcavenae/marvel/internal/runtime/claudecode"
	"github.com/arcavenae/marvel/internal/runtime/codex"
	"github.com/arcavenae/marvel/internal/runtime/opencode"
)

// TestProfileResolvesForAdapterName pins the two harness namespaces
// together, because profileFor is reached from both and neither reaches
// it loudly.
//
// An event carries the parser package's Harness const; a session's
// launch-time bind carries api.Session.Runtime.Name, which is the
// adapter's Name(). Claude spelled those "claude-code" and "claude"
// respectively, so the bind stored a zero-value profile. Nothing failed:
// the stored profile is never read today, and Bind discards the
// not-found bool. A divergence with no symptom needs a test, which is
// why this asserts the contract rather than an observable.
func TestProfileResolvesForAdapterName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		adapter runtime.Adapter
		harness string
	}{
		{&runtime.Claude{}, claudecode.Harness},
		{&runtime.Codex{}, codex.Harness},
		{&runtime.OpenCode{}, opencode.Harness},
	}

	for _, tc := range cases {
		t.Run(tc.harness, func(t *testing.T) {
			t.Parallel()
			name := tc.adapter.Name()
			if name != tc.harness {
				t.Errorf("adapter name %q and parser harness %q disagree; profileFor is keyed on both, so a bound session gets a zero profile", name, tc.harness)
			}
			if _, ok := profileFor(name); !ok {
				t.Errorf("no usage profile registered under adapter name %q", name)
			}
			if _, ok := profileFor(tc.harness); !ok {
				t.Errorf("no usage profile registered under parser harness %q", tc.harness)
			}
		})
	}
}
