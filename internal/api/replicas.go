package api

import (
	"fmt"
	"math"
)

// MinScaleReplicas is the lowest replica count an operator may set on a
// running team. Negative counts are refused: planRole computed its
// scale-down excess from one and indexed past the session list, panicking
// the daemon, and because the count was stored first the next daemon
// panicked again at its first reconcile (marvel#365).
//
// Zero is legal desired state: it parks the role (marvel#335). The
// manifest parser and marvel scale share this bound through
// ValidateReplicas, so the two doors cannot disagree again.
const MinScaleReplicas = 0

// MaxScaleReplicas is the highest replica count one role may declare or be
// scaled to. It is far above any real team: it exists to stop an integer sum
// against a session ceiling from wrapping, not to size teams. A count over it
// is refused by apply and scale, and a stored one is reported at startup and
// left as it is.
const MaxScaleReplicas = 10000

// ValidateReplicas refuses a replica count below MinScaleReplicas or above
// MaxScaleReplicas, naming the team and role so the operator knows which
// declaration to fix. It is a structural check (ADR-007), so refusing is
// correct.
func ValidateReplicas(team, role string, n int) error {
	if n < MinScaleReplicas {
		return fmt.Errorf("team %s role %s: replicas %d is below the minimum of %d", team, role, n, MinScaleReplicas)
	}
	if n > MaxScaleReplicas {
		return fmt.Errorf("team %s role %s: replicas %d is above the maximum of %d (MaxScaleReplicas)", team, role, n, MaxScaleReplicas)
	}
	return nil
}

// AddSat returns a+b, held at math.MaxInt or math.MinInt instead of wrapping.
// Replica counts and session counts are summed against a session ceiling, and
// a wrapped sum reads as under the limit; a held one reads as over it.
func AddSat(a, b int) int {
	switch {
	case b > 0 && a > math.MaxInt-b:
		return math.MaxInt
	case b < 0 && a < math.MinInt-b:
		return math.MinInt
	}
	return a + b
}
