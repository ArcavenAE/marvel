package daemon

import "github.com/arcavenae/marvel/internal/bus"

// leafGate is the controller's LeafUp: true only while the bus supervisor
// reads the cluster's leaf connection to the hub as up. "down", "detached",
// "unenrolled", "n/a" (no hub), "unknown" (not polled yet) and anything else
// read as off, so the director leg of an escalation is never on by default.
func leafGate(status func() bus.Status) func() bool {
	return func() bool { return status().Leaf == "up" }
}
