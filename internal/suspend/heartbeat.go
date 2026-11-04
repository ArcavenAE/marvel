package suspend

import "time"

// PauseOf is how long a suspended cluster has been paused: now minus the
// record's Since. This is the one place the measure is defined, so changing it
// changes it everywhere. Since is written a moment before the first stop, so
// the pause is over-counted by the collection and stop time; that is the safe
// direction, since at worst a seat that really died is noticed that many
// seconds later and no live seat is restarted for the pause. A clock stepped
// back gives a negative pause, which ShiftedHeartbeat treats as none.
func PauseOf(rec Record, now time.Time) time.Duration {
	return now.Sub(rec.Since)
}

// ShiftedHeartbeat returns last moved forward by pause, never past now. A zero
// stamp stays zero (the first-heartbeat grace is a later change), a pause that
// is not positive shifts nothing, and a stamp already ahead of now is left
// alone.
func ShiftedHeartbeat(last time.Time, pause time.Duration, now time.Time) time.Time {
	if last.IsZero() || pause <= 0 || last.After(now) {
		return last
	}
	shifted := last.Add(pause)
	if shifted.After(now) {
		return now
	}
	return shifted
}
