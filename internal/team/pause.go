package team

import (
	"fmt"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/suspend"
)

// ShiftForPause moves the controller's timers forward by pause after a fleet
// suspend (docs/design/fleet-suspend-resume.md, SR-F): the tick rings, and each
// role's backoff deadline and healthy streak. The saturation freeze keeps its
// sentinel and zero values stay zero. A role's persisted record is rewritten
// with the shifted deadline. Applying the same pause twice moves a deadline
// and a streak further out, which only lengthens a backoff and delays a decay;
// the sessions' own counters are guarded separately by the daemon.
func (c *Controller) ShiftForPause(pause time.Duration, now time.Time) {
	if pause <= 0 {
		return
	}
	c.ringMu.Lock()
	for _, r := range c.rings {
		r.Shift(pause, now)
	}
	c.ringMu.Unlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	for key, rh := range c.roleHealth {
		if !rh.BackoffUntil.Equal(saturationFreezeUntil) {
			rh.BackoffUntil = suspend.ShiftedDeadline(rh.BackoffUntil, pause)
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
