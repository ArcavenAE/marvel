package daemon

import (
	"testing"

	"github.com/arcavenae/marvel/internal/bus"
)

// The director leg of an escalation is on only while the leaf reads "up".
func TestLeafGateIsOnOnlyWhileTheLeafIsUp(t *testing.T) {
	t.Parallel()
	for leaf, want := range map[string]bool{
		"up":         true,
		"down":       false,
		"detached":   false,
		"unenrolled": false,
		"n/a":        false,
		"unknown":    false,
		"":           false,
	} {
		gate := leafGate(func() bus.Status { return bus.Status{Leaf: leaf} })
		if got := gate(); got != want {
			t.Errorf("leaf %q: gate = %v, want %v", leaf, got, want)
		}
	}
}

// The gate reads the status each time it is asked, so a leaf that goes down
// turns the leg off.
func TestLeafGateFollowsTheStatusEachTime(t *testing.T) {
	t.Parallel()
	leaf := "up"
	gate := leafGate(func() bus.Status { return bus.Status{Leaf: leaf} })
	if !gate() {
		t.Fatal("gate off while the leaf is up")
	}
	leaf = "down"
	if gate() {
		t.Error("gate still on after the leaf went down")
	}
}
