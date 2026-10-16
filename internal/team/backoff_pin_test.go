package team

import (
	"testing"
	"time"
)

// pinnedRestartWait is the restart wait both crash loops are meant to share:
// 30s up to the first restart, then doubling to a 5m cap. internal/bus pins
// the same literal for its copy, so the two copies are held to one table.
func pinnedRestartWait(n int) time.Duration {
	switch {
	case n <= 1:
		return 30 * time.Second
	case n == 2:
		return time.Minute
	case n == 3:
		return 2 * time.Minute
	case n == 4:
		return 4 * time.Minute
	default:
		return 5 * time.Minute
	}
}

func TestComputeBackoffMatchesThePinnedSchedule(t *testing.T) {
	t.Parallel()
	for n := -3; n <= 60; n++ {
		if got, want := computeBackoff(n), pinnedRestartWait(n); got != want {
			t.Errorf("computeBackoff(%d) = %s, want %s", n, got, want)
		}
	}
}
