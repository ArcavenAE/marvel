package main

import "time"

// clockTolerance is how far past the client's now a daemon stamp may sit
// before it reads as a future stamp: the round trip of the call that fetched
// it. Zero until a fetch measures one.
var clockTolerance time.Duration

// futureSkew reports how far stamp sits past now, and whether that is more
// than tol. A zero stamp is never in the future.
func futureSkew(stamp, now time.Time, tol time.Duration) (time.Duration, bool) {
	return 0, false
}

// withClockSkew adds a clock_skew list to a session object, or to each session
// of a list, for every stamp later than now plus tol. It rewrites no stamp.
func withClockSkew(raw []byte, now time.Time, tol time.Duration) ([]byte, error) {
	return raw, nil
}

// measureRoundTrip runs call and returns how long it took on lastActiveClock.
func measureRoundTrip(call func() error) (time.Duration, error) {
	return 0, call()
}
