package daemon

import (
	"testing"
	"time"
)

// Once the strip has finished, a spawn carries no note, even when an earlier spawn
// had waited the whole bound. With the strip done and the bound at zero, the gate
// must not pick the zero timer over the closed channel.
func TestAFinishedStripLeavesNoNoteOnALateSpawn(t *testing.T) {
	d := &Daemon{
		seatStripDone:  make(chan struct{}),
		seatStripLate:  true,
		seatStripBound: time.Second,
		seatSecretVars: map[string]string{"bd": "BEADS_DOLT_PASSWORD"},
	}
	close(d.seatStripDone)
	for i := 0; i < 500; i++ {
		if note := d.spawnGate(); note != "" {
			t.Fatalf("call %d returned the note %q after the strip had finished", i, note)
		}
	}
}
