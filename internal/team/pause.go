package team

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/suspend"
)

// ShiftForPause moves the controller's timers forward by pause after a fleet
// suspend that began at since (docs/design/fleet-suspend-resume.md, SR-F): the
// tick rings, and each role's backoff deadline and healthy streak. A backoff
// that had already expired at since stays as it was. The saturation freeze
// keeps its sentinel and zero values stay zero. A role's persisted record is rewritten
// with the shifted deadline. Applying the same pause twice moves a deadline
// and a streak further out, which only lengthens a backoff and delays a decay;
// the sessions' own counters are guarded separately by the daemon.
func (c *Controller) ShiftForPause(pause time.Duration, since, now time.Time) error {
	if pause <= 0 {
		return nil
	}
	c.suspended.set(since, now)
	c.ringMu.Lock()
	for _, r := range c.rings {
		r.Shift(pause, now)
	}
	c.ringMu.Unlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	for key, rh := range c.roleHealth {
		if !rh.BackoffUntil.Equal(saturationFreezeUntil) {
			rh.BackoffUntil = suspend.ShiftedDeadline(rh.BackoffUntil, since, pause)
		}
		rh.HealthySince = suspend.ShiftedHeartbeat(rh.HealthySince, pause, now)
		c.persistRoleHealth(key, rh)
	}
	return c.shiftScheduleStatuses(pause, since)
}

// shiftScheduleStatuses keeps a pause out of the freshness alarm and out of a
// retry wait, with no recorded stamp moved: FiringDueAt, Since and
// LastSucceededAt are facts the status and the run history report. The pause is
// added to PausedSinceAnchor once per suspend record (PauseAppliedSince holds
// the one already counted), and RetryAfter moves only when it was still ahead
// when the suspend began. A schedule's own clock is left alone: it behaves as
// after any pause (docs/design/scheduled-runs.md, section 3). A cadence status
// holds no last-seen stamp or alarm, so it needs nothing.
func (c *Controller) shiftScheduleStatuses(pause time.Duration, since time.Time) error {
	var errs []error
	for _, st := range c.store.ListScheduleStatus() {
		if _, err := c.store.UpdateScheduleStatus(st.Key, func(st *api.ScheduleStatus) bool {
			if st.PauseAppliedSince.Equal(since) {
				return false
			}
			st.PausedSinceAnchor += pause
			st.PauseAppliedSince = since
			st.RetryAfter = suspend.ShiftedDeadline(st.RetryAfter, since, pause)
			return true
		}); err != nil {
			errs = append(errs, fmt.Errorf("shift schedule status %s: %w", st.Key, err))
		}
	}
	return errors.Join(errs...)
}

// pausedNote says how much of a session's age was a fleet suspend, so the age a
// message prints is the age the check used. It is empty when there was none.
func pausedNote(s api.Session) string { return pausedExcluded(s.PausedTotal) }

// pausedExcluded is the clause a message adds when d of the time it measures
// was a fleet suspend, and nothing when there was none.
func pausedExcluded(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return fmt.Sprintf(" (paused %s excluded)", d.Round(time.Second))
}

// suspendWindow is the span of the last fleet suspend a resume applied: from the
// moment it began to the moment it ended. The schedule clock reads it to say
// that a firing was missed during a suspend and not while the daemon was down.
// Each schedule reads it at its first clock step after the resume and never
// again, so an unrelated miss days later does not carry the clause. It lives in
// memory, so a daemon that restarts before that step has forgotten it and the
// message falls back to the plain one, which is true and less specific.
type suspendWindow struct {
	mu       sync.Mutex
	since    time.Time
	end      time.Time
	consumed map[string]bool
}

func (s *suspendWindow) set(since, end time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.since, s.end = since, end
	s.consumed = make(map[string]bool)
}

// take is a schedule's first look at the window after a resume. It returns the
// window while key has not looked yet and marks it looked, so every later step
// of that schedule sees none.
func (s *suspendWindow) take(key string) (since, end time.Time, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.since.IsZero() || s.consumed[key] {
		return time.Time{}, time.Time{}, false
	}
	s.consumed[key] = true
	return s.since, s.end, true
}
