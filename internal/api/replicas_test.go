package api

import (
	"math"
	"strings"
	"testing"
)

func TestValidateReplicas(t *testing.T) {
	t.Parallel()
	tests := []struct {
		n       int
		wantErr bool
	}{
		{-5, true},
		{-1, true},
		{0, false},
		{3, false},
		{MaxScaleReplicas, false},
		{MaxScaleReplicas + 1, true},
		{math.MaxInt, true},
	}
	for _, tt := range tests {
		err := ValidateReplicas("ws/crew", "reviewer", tt.n)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateReplicas(%d) = %v, wantErr %v", tt.n, err, tt.wantErr)
		}
		if err != nil && (!strings.Contains(err.Error(), "ws/crew") || !strings.Contains(err.Error(), "reviewer")) {
			t.Errorf("error %q does not name the team and role", err)
		}
	}
}

// The upper bound names itself and its limit, so an operator who hits it knows
// which number to lower.
func TestValidateReplicasAboveTheBoundNamesTheLimit(t *testing.T) {
	t.Parallel()
	err := ValidateReplicas("ws/crew", "reviewer", MaxScaleReplicas+1)
	if err == nil || !strings.Contains(err.Error(), "MaxScaleReplicas") || !strings.Contains(err.Error(), "10000") {
		t.Fatalf("error = %v, want it to name MaxScaleReplicas and the limit", err)
	}
}

func TestAddSatHoldsAtTheEndsInsteadOfWrapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		a, b, want int
	}{
		{1, 2, 3},
		{-1, 1, 0},
		{math.MaxInt, 1, math.MaxInt},
		{1, math.MaxInt, math.MaxInt},
		{math.MaxInt, math.MaxInt, math.MaxInt},
		{math.MinInt, -1, math.MinInt},
		{math.MaxInt - 1, 1, math.MaxInt},
	}
	for _, tt := range tests {
		if got := AddSat(tt.a, tt.b); got != tt.want {
			t.Errorf("AddSat(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

// The manifest's declaration clause sums replicas against max_sessions. Sums
// that wrap negative read as under the ceiling, so they must be held.
func TestManifestBudgetRefusesDeclarationsThatWouldWrap(t *testing.T) {
	t.Parallel()
	team := ManifestTeam{
		Name:   "crew",
		Budget: &ManifestBudget{MaxSessions: 6},
		Roles: []ManifestRole{
			{Name: "a", Replicas: math.MaxInt},
			{Name: "b", Replicas: math.MaxInt},
		},
	}
	if err := validateManifestBudget(0, team); err == nil {
		t.Fatal("a team declaring MaxInt replicas in two roles passed a ceiling of 6")
	}
}
