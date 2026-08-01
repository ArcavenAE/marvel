package main

import (
	"testing"
	"time"
)

// TestResolveShiftTimeout covers the precedence the daemon uses for its
// shift timeout: the --shift-timeout flag wins when set; otherwise
// MARVEL_SHIFT_TIMEOUT is parsed; otherwise zero, which leaves the team
// controller on its built-in 10-minute default. See aae-orc-sape.
func TestResolveShiftTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		flagVal     time.Duration
		flagChanged bool
		env         string
		want        time.Duration
		wantErr     bool
	}{
		{name: "unset falls back to zero (controller default)", want: 0},
		{name: "flag set wins", flagVal: 90 * time.Second, flagChanged: true, want: 90 * time.Second},
		{name: "flag wins over env", flagVal: 30 * time.Second, flagChanged: true, env: "5m", want: 30 * time.Second},
		{name: "env used when flag unset", env: "45s", want: 45 * time.Second},
		{name: "env minutes", env: "2m", want: 2 * time.Minute},
		{name: "invalid env errors", env: "not-a-duration", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveShiftTimeout(tt.flagVal, tt.flagChanged, tt.env)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (value %s)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("resolveShiftTimeout(%s, %v, %q) = %s, want %s",
					tt.flagVal, tt.flagChanged, tt.env, got, tt.want)
			}
		})
	}
}
