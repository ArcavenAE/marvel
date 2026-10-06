package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemon"
)

// unknownClusterFixture gives the client a scratch home with one named
// cluster and a listener on the default socket that counts connections,
// so a test can tell "refused" from "dialed the local daemon". It returns
// the connection counter.
func unknownClusterFixture(t *testing.T) *atomic.Int32 {
	t.Helper()
	// A short home keeps the unix socket path under the platform limit.
	home, err := os.MkdirTemp("", "mv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	t.Setenv(config.SocketEnv, "")
	oldSocket, oldCluster := socketPath, clusterName
	socketPath, clusterName = "", ""
	t.Cleanup(func() { socketPath, clusterName = oldSocket, oldCluster })

	cfg := &config.Config{
		Clusters:       []config.Cluster{{Name: "testcluster", Socket: filepath.Join(home, "cluster.sock")}},
		CurrentCluster: "testcluster",
	}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	sock := config.DefaultSocket()
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen on the default socket: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var dialed atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			dialed.Add(1)
			_ = conn.Close()
		}
	}()
	return &dialed
}

// A --cluster name that is not in the config refuses and dials nothing.
// Before this, a typo warned and then ran the command on the local daemon
// (#502).
func TestUnknownClusterRefuses(t *testing.T) {
	dialed := unknownClusterFixture(t)
	clusterName = "remote-v"

	_, err := send(daemon.Request{Method: "status"})
	if err == nil {
		t.Fatal("send succeeded against an unknown cluster, want a refusal")
	}
	if !strings.Contains(err.Error(), `unknown cluster "remote-v"`) {
		t.Errorf("error = %q, want it to name the unknown cluster", err)
	}
	if n := dialed.Load(); n != 0 {
		t.Errorf("the local daemon was dialed %d time(s), want 0", n)
	}
}

// Pins today's behavior, not a decision: a config whose own current_cluster
// names nothing still warns and falls back to the local daemon. Whether it
// should refuse too is an open question for the operator.
func TestUnknownCurrentClusterTodayFallsBackLocal(t *testing.T) {
	_ = unknownClusterFixture(t)
	cfg := &config.Config{
		Clusters:       []config.Cluster{{Name: "testcluster", Socket: "/scratch/cluster.sock"}},
		CurrentCluster: "gone",
	}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	addr, _, err := resolveDaemonAddr()
	if err != nil {
		t.Fatalf("resolveDaemonAddr: %v, want the local fallback and no error", err)
	}
	if addr != config.DefaultSocket() {
		t.Errorf("addr = %q, want the default socket %q", addr, config.DefaultSocket())
	}
}
