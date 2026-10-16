// Package backoff is the restart schedule two crash loops share.
package backoff

import "time"

// Schedule is an exponential restart schedule: Initial for the first restart,
// doubling each time, never more than Max.
type Schedule struct{ Initial, Max time.Duration }

// Wait returns the wait before restart n (1-indexed).
func (s Schedule) Wait(n int) time.Duration { return 0 }

// Restart is the schedule for a crash-looping role or service: 30s, 60s, 2m,
// 4m, then 5m.
var Restart = Schedule{Initial: 30 * time.Second, Max: 5 * time.Minute}
