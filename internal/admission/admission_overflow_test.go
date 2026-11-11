package admission

import (
	"math"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// The session ceiling is the one runtime guard against a runaway team. A
// replica count near the largest int must not wrap the sum negative and read as
// within the limit.
func TestCountClauseRefusesASumThatWouldWrap(t *testing.T) {
	t.Parallel()
	b := api.Budget{MaxSessions: 6}
	for _, live := range []int{1, 2, 6} {
		v := Check(b, metered(live, live, 0), Request{Role: "crew", Want: math.MaxInt, Kind: Growth})
		if !v.Refused() {
			t.Errorf("live %d, want MaxInt: decision %v, want a refusal", live, v.Decision)
		}
		if v.Granted > 0 {
			t.Errorf("live %d, want MaxInt: granted %d, want none", live, v.Granted)
		}
	}
}
