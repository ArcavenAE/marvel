package api

import (
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
