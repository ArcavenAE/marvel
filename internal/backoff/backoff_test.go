package backoff

import (
	"math"
	"testing"
	"time"
)

func TestRestartMatchesTheRoleSchedule(t *testing.T) {
	t.Parallel()
	want := []time.Duration{30 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute, 5 * time.Minute}
	for n, w := range want {
		if got := Restart.Wait(n); got != w {
			t.Errorf("Restart.Wait(%d) = %s, want %s", n, got, w)
		}
	}
}

// A shift past the width of the integer wraps to zero or to a negative
// duration; the cap holds there too.
func TestWaitStaysAtTheCapWhereTheShiftOverflows(t *testing.T) {
	t.Parallel()
	for _, n := range []int{34, 60, 64, 65, 1000, math.MaxInt} {
		if got := Restart.Wait(n); got != Restart.Max {
			t.Errorf("Restart.Wait(%d) = %s, want the cap %s", n, got, Restart.Max)
		}
	}
}

func TestWaitHonoursTheScheduleItIsGiven(t *testing.T) {
	t.Parallel()
	s := Schedule{Initial: 200 * time.Millisecond, Max: time.Second}
	want := map[int]time.Duration{
		-3: 200 * time.Millisecond,
		0:  200 * time.Millisecond,
		1:  200 * time.Millisecond,
		2:  400 * time.Millisecond,
		3:  800 * time.Millisecond,
		4:  time.Second,
		50: time.Second,
	}
	for n, w := range want {
		if got := s.Wait(n); got != w {
			t.Errorf("Wait(%d) = %s, want %s", n, got, w)
		}
	}
	flat := Schedule{Initial: time.Hour, Max: time.Hour}
	for _, n := range []int{1, 2, 9} {
		if got := flat.Wait(n); got != time.Hour {
			t.Errorf("flat Wait(%d) = %s, want one hour", n, got)
		}
	}
}
