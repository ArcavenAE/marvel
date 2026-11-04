package suspend

import (
	"testing"
	"time"
)

// The heartbeat shift (docs/design/fleet-suspend-resume.md SR-F, heartbeat
// part): a pause moves a last-seen stamp forward by its length, so a seat that
// heartbeated just before the stop is not stale the moment it resumes.
func TestShiftedHeartbeat(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	cases := map[string]struct {
		last  time.Time
		pause time.Duration
		want  time.Time
	}{
		"moved by the pause": {now.Add(-40 * time.Minute), 30 * time.Minute, now.Add(-10 * time.Minute)},
		// A seat that heartbeated between the start of the suspend and its own
		// stop would otherwise get a stamp in the future.
		"capped at now":          {now.Add(-5 * time.Minute), 30 * time.Minute, now},
		"zero stays zero":        {time.Time{}, 30 * time.Minute, time.Time{}},
		"negative shifts none":   {now.Add(-time.Minute), -5 * time.Minute, now.Add(-time.Minute)},
		"zero pause shifts none": {now.Add(-time.Minute), 0, now.Add(-time.Minute)},
		// A stamp already ahead of now is left as it is, not pulled back.
		"future stamp untouched": {now.Add(time.Minute), 30 * time.Minute, now.Add(time.Minute)},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := ShiftedHeartbeat(c.last, c.pause, now); !got.Equal(c.want) {
				t.Errorf("ShiftedHeartbeat = %v, want %v", got, c.want)
			}
		})
	}
}

// PauseOf is now minus Since, read once by the caller: a suspend that started
// after now (a clock stepped back) is a negative pause, which shifts nothing.
func TestPauseOf(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	if got := PauseOf(Record{Since: now.Add(-90 * time.Second)}, now); got != 90*time.Second {
		t.Errorf("PauseOf = %v, want 90s", got)
	}
	if got := PauseOf(Record{Since: now.Add(time.Hour)}, now); got >= 0 {
		t.Errorf("PauseOf = %v for a Since in the future, want negative", got)
	}
}
