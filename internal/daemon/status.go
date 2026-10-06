package daemon

import (
	"encoding/json"
	"net"

	"github.com/arcavenae/marvel/internal/bus"
	"github.com/arcavenae/marvel/internal/config"
)

// MRVLState is how far the mrvl:// listener reaches, read from the address the
// daemon actually bound and never from flag text or client config.
type MRVLState string

const (
	// MRVLOff means no mrvl:// listener: the unix socket only.
	MRVLOff MRVLState = "off"
	// MRVLLoopback means the listener is bound to a loopback address.
	MRVLLoopback MRVLState = "loopback"
	// MRVLNetwork means any other bind, and a wildcard bind counts.
	MRVLNetwork MRVLState = "network"
)

// MRVLStatus is the mrvl:// half of the daemon status.
type MRVLStatus struct {
	State MRVLState `json:"state"`
	// Addr is the bound address, host:port, empty when the listener is off.
	Addr string `json:"addr,omitempty"`
}

// DaemonStatus is the answer to daemon.status: who this daemon is and what it
// is reachable through. It describes the daemon that answered, not a session
// (docs/design/get-sessions-output.md section 2). Diagnostic only.
type DaemonStatus struct {
	// Home is the layout home the daemon is rooted at.
	Home string `json:"home,omitempty"`
	// Cluster is the name the daemon's own config gives the cluster it
	// serves, empty when the config names none.
	Cluster string `json:"cluster,omitempty"`
	// MRVL is the mrvl:// listener.
	MRVL MRVLStatus `json:"mrvl"`
	// Bus is the same value bus.status returns, absent when the cluster has
	// no bus.
	Bus *bus.Status `json:"bus,omitempty"`
}

// classifyBind maps a bound address to its reach.
func classifyBind(addr net.Addr) MRVLState {
	return MRVLOff
}

// identifyCluster records the name the daemon's config gives the cluster
// that listens on socketPath.
func (d *Daemon) identifyCluster(socketPath string) {
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return
	}
	cl := cfg.ClusterForSocket(socketPath)
	_ = cl
}

// handleDaemonStatus answers daemon.status.
func (d *Daemon) handleDaemonStatus() Response {
	data, err := json.Marshal(DaemonStatus{})
	if err != nil {
		return Response{Error: err.Error()}
	}
	return Response{Result: data}
}
