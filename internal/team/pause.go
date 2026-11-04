package team

import (
	"fmt"
	"sync/atomic"
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
func (c *Controller) ShiftForPause(pause time.Duration, since, now time.Time) {
	if pause <= 0 {
		return
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
}

// pausedNote says how much of a session's age was a fleet suspend, so the age a
// message prints is the age the check used. It is empty when there was none.
func pausedNote(s api.Session) string {
	if s.PausedTotal <= 0 {
		return ""
	}
	return fmt.Sprintf(" (paused %s excluded)", s.PausedTotal.Round(time.Second))
}

// suspendWindow is the span of the last fleet suspend a resume applied: from the
// moment it began to the moment it ended. The schedule clock reads it to say
// that a firing was missed during a suspend and not while the daemon was down.
// It lives in memory, so a daemon that restarts after the resume has forgotten
// it and the message falls back to the plain one.
type suspendWindow struct{ w atomic.Pointer[[2]time.Time] }

func (s *suspendWindow) set(since, end time.Time) {
	s.w.Store(&[2]time.Time{since, end})
}

// contains reports the start of the suspend when at fell inside it, from the
// moment it began to the moment it ended.
func (s *suspendWindow) contains(at time.Time) (since time.Time, ok bool) {
	w := s.w.Load()
	if w == nil || at.Before(w[0]) || at.After(w[1]) {
		return time.Time{}, false
	}
	return w[0], true
}
