package team

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// For a session marvel has observed (ContextAt non-zero) with a timeout set,
// evaluateActivity says Stalled exactly when the shared predicate says quiet.
// They are one test, so the rate, ACTIVE% and the advisory cannot disagree.
func TestQuietPredicateMatchesEvaluateActivityForObservedSessions(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	const window = 10 * time.Minute
	role := &api.Role{Name: "worker", ActivityTimeout: window}
	for _, age := range []time.Duration{
		0, time.Second, window - time.Nanosecond, window, window + time.Nanosecond, time.Hour,
	} {
		s := &api.Session{Runtime: bareInteractiveRuntime(), CreatedAt: now.Add(-2 * time.Hour)}
		s.ContextAt = now.Add(-age)
		stalled := evaluateActivity(s, role, now) == api.ActivityStalled
		if quiet := api.Quiet(s, window, now); stalled != quiet {
			t.Errorf("age %v: evaluateActivity stalled=%v, Quiet=%v; they must agree", age, stalled, quiet)
		}
	}
}
