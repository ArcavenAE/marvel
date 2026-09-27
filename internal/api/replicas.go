package api

import "fmt"

// MinScaleReplicas is the lowest replica count an operator may set on a
// running team. Negative counts are refused: planRole computed its
// scale-down excess from one and indexed past the session list, panicking
// the daemon, and because the count was stored first the next daemon
// panicked again at its first reconcile (marvel#365).
//
// Whether zero is legal is an open question (marvel#335); the manifest
// parser still requires at least 1. When that is ruled, this constant is
// the one place scale's lower bound changes.
const MinScaleReplicas = 0

// ValidateReplicas refuses a replica count below MinScaleReplicas, naming
// the team and role so the operator knows which declaration to fix. It is a
// structural check (ADR-007), so refusing is correct.
func ValidateReplicas(team, role string, n int) error {
	if n < MinScaleReplicas {
		return fmt.Errorf("team %s role %s: replicas %d is below the minimum of %d", team, role, n, MinScaleReplicas)
	}
	return nil
}
