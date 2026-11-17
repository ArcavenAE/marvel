package bus

import "time"

// HubReading is one read of a hub's loopback /leafz (aae-orc-stqn9).
type HubReading struct {
	// ObservedAt is when the read succeeded; zero when none has.
	ObservedAt time.Time
	// Leafs is the leaf names the hub listed.
	Leafs []string
	// Err is why the last read failed or timed out; empty on success.
	Err string
}

// HubStatusLines renders a hub reading as diagnostic lines for bus status.
// This is the compile-only stub the red tests build against.
func HubStatusLines(r HubReading, expected []string, own string, now time.Time, validFor time.Duration) []string {
	return nil
}
