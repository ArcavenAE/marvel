package main

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// An idle statusline seat beats every 15 seconds, so ContextAt reads seconds old
// while its reading last changed half an hour ago. LAST-ACTIVE is the time since
// the seat showed work, so it reads the reading change (marvel#801).
func TestLastActiveReadsTheReadingChangeNotTheBeat(t *testing.T) {
	s := activeSession("a", 15*time.Second, api.ContextSourceHeartbeat)
	s.ReadingChangedAt = lastActiveNow.Add(-30 * time.Minute)
	if got := lastActiveCell(s, lastActiveNow); got != "30m*" {
		t.Errorf("cell = %q for a seat that beat 15s ago and last changed 30m ago, want 30m*", got)
	}
	// A producer that never wrote the stamp reads as before.
	s.ReadingChangedAt = time.Time{}
	if got := lastActiveCell(s, lastActiveNow); got != "15s*" {
		t.Errorf("cell = %q with no work stamp, want 15s*", got)
	}
}
