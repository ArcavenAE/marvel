package api

import (
	"testing"
	"time"
)

// The role's own activity_timeout is the window when it declares one;
// otherwise the cluster's quiet window; otherwise the default. A role with a
// timeout never reads the cluster's.
func TestQuietWindowDefaultsWhenNoActivityTimeout(t *testing.T) {
	if DefaultQuietWindow != 10*time.Minute {
		t.Fatalf("DefaultQuietWindow = %v, want the design's 10 minutes", DefaultQuietWindow)
	}
	cases := []struct {
		name    string
		role    *Role
		cluster time.Duration
		want    time.Duration
	}{
		{"no role, no cluster window", nil, 0, DefaultQuietWindow},
		{"role without a timeout", &Role{}, 0, DefaultQuietWindow},
		{"role timeout wins", &Role{ActivityTimeout: 90 * time.Second}, 3 * time.Minute, 90 * time.Second},
		{"cluster window when the role declares none", &Role{}, 3 * time.Minute, 3 * time.Minute},
		{"cluster window with no role", nil, 3 * time.Minute, 3 * time.Minute},
		{"a negative cluster window is not a window", &Role{}, -time.Minute, DefaultQuietWindow},
		{"a negative role timeout is not a timeout", &Role{ActivityTimeout: -time.Second}, 0, DefaultQuietWindow},
	}
	for _, tc := range cases {
		if got := QuietWindow(tc.role, tc.cluster); got != tc.want {
			t.Errorf("%s: QuietWindow = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A session is quiet when its ContextAt is zero or strictly older than the
// window. At exactly the window it is not quiet yet, the same boundary
// evaluateActivity has always had.
func TestQuietPredicateBoundary(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	w := 10 * time.Minute
	at := func(age time.Duration) *Session {
		s := &Session{}
		s.ContextAt = now.Add(-age)
		return s
	}
	cases := []struct {
		name string
		s    *Session
		want bool
	}{
		{"never observed", &Session{}, true},
		{"just now", at(0), false},
		{"inside the window", at(w - time.Nanosecond), false},
		{"exactly the window", at(w), false},
		{"just past the window", at(w + time.Nanosecond), true},
		{"a clock that stepped back", at(-time.Minute), false},
	}
	for _, tc := range cases {
		if got := Quiet(tc.s, w, now); got != tc.want {
			t.Errorf("%s: Quiet = %v, want %v", tc.name, got, tc.want)
		}
	}
	// A zero ContextAt is quiet by its own rule, not because the subtraction
	// happens to overflow past the window: even a window as long as a
	// Duration can be leaves a never-observed session quiet.
	if !Quiet(&Session{}, time.Duration(1<<63-1), now) {
		t.Error("a never-observed session is quiet whatever the window")
	}
}
