package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"slices"
	"time"

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
	// AdoptingSince is when the daemon began serving ahead of start-time
	// adoption. Present only while adoption is still running.
	AdoptingSince *time.Time `json:"adopting_since,omitempty"`
}

// classifyBind maps a bound address to its reach.
func classifyBind(addr net.Addr) MRVLState {
	if addr == nil {
		return MRVLOff
	}
	var ip net.IP
	switch a := addr.(type) {
	case *net.TCPAddr:
		ip = a.IP
	default:
		host, _, err := net.SplitHostPort(addr.String())
		if err != nil {
			return MRVLNetwork
		}
		ip = net.ParseIP(host)
	}
	// A bind that names no host, or one this code cannot read, is not
	// shown as local: the safe wrong answer is network.
	if ip == nil || ip.IsUnspecified() || !ip.IsLoopback() {
		return MRVLNetwork
	}
	return MRVLLoopback
}

// identifyCluster records the name the daemon's config gives the cluster
// that listens on socketPath.
func (d *Daemon) identifyCluster(socketPath string) {
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return
	}
	first := cfg.ClusterForSocket(socketPath)
	if first == nil {
		return
	}
	// ClusterForSocket returns the first entry that matches. When a second
	// one also matches (two entries on one path, or a socketless entry beside
	// an alias at the default), the daemon cannot tell which one a client
	// came in through, so it reports no name and a client sees no mismatch it
	// did not cause. The same function is shared with the schedule cap and the
	// services attach, so the check is made here, by asking again without the
	// first match.
	rest := &config.Config{Clusters: slices.DeleteFunc(slices.Clone(cfg.Clusters), func(c config.Cluster) bool {
		return c.Name == first.Name
	})}
	if other := rest.ClusterForSocket(socketPath); other != nil {
		log.Printf("cluster identity: entries %q and %q both name %s; reporting no cluster name", first.Name, other.Name, socketPath)
		return
	}
	d.cluster = first.Name
}

// handleDaemonStatus answers daemon.status.
func (d *Daemon) handleDaemonStatus() Response {
	st := DaemonStatus{Home: d.home, Cluster: d.cluster, MRVL: MRVLStatus{State: MRVLOff}}
	if srv := d.sshServer.Load(); srv != nil {
		if addr := srv.Addr(); addr != nil {
			st.MRVL = MRVLStatus{State: classifyBind(addr), Addr: addr.String()}
		}
	}
	if b, ok := d.busStatus(); ok {
		st.Bus = &b
	}
	if ns := d.adoptingSince.Load(); ns != 0 {
		since := time.Unix(0, ns).UTC()
		st.AdoptingSince = &since
	}
	data, err := json.Marshal(st)
	if err != nil {
		return Response{Error: fmt.Sprintf("encode daemon status: %v", err)}
	}
	return Response{Result: data}
}
