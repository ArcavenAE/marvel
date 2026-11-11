package team

import (
	"math"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// declaredSessions feeds the session ceiling, so a sum of stored counts that
// wraps negative would read as within it.
func TestDeclaredSessionsHoldsAtMaxInt(t *testing.T) {
	t.Parallel()
	tm := &api.Team{Roles: []api.Role{{Name: "a", Replicas: math.MaxInt}, {Name: "b", Replicas: 5}}}
	c := &Controller{}
	if got := c.declaredSessions(tm); got != math.MaxInt {
		t.Fatalf("declaredSessions = %d, want it held at %d", got, math.MaxInt)
	}
}
