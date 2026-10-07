package team

import "time"

// TickRingWindow is how much evaluation history a session's ring holds
// (docs/design/get-sessions-output.md section 4.4).
const TickRingWindow = 15 * time.Minute

// TickRing is one session's last TickRingWindow of evaluation ticks, in memory.
type TickRing struct{ samples []tickSample }

type tickSample struct{ at, contextAt time.Time }

// Record notes one evaluation tick at at, when the session's ContextAt read
// contextAt (zero when never observed).
func (r *TickRing) Record(at, contextAt time.Time) {
	r.samples = append(r.samples, tickSample{at: at, contextAt: contextAt})
}

// Counts returns, over the ticks in the last TickRingWindow before now, how many
// found the session not quiet under window and how many there were. full is
// false until the ring has watched for the whole TickRingWindow, which a daemon
// restart resets.
func (r *TickRing) Counts(window time.Duration, now time.Time) (active, total int, full bool) {
	return 0, 0, false
}

// ActiveTicks is the counts for one session's ring, or zero and not full for a
// session the controller has not evaluated.
func (c *Controller) ActiveTicks(sessKey string, window time.Duration, now time.Time) (active, total int, full bool) {
	return 0, 0, false
}
