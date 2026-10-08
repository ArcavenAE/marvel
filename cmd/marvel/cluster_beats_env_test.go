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

// countingSocket listens on a unix socket in dir and counts the
// connections it accepts, so a test can tell which daemon a command reached.
func countingSocket(t *testing.T, dir, name string) (string, *atomic.Int32) {
	t.Helper()
	path := filepath.Join(dir, name)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen on %s: %v", name, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var n atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			n.Add(1)
			_ = conn.Close()
		}
	}()
	return path, &n
}

// envClusterFixture gives the client a scratch home with two clusters, the
// current one and another, each behind a counting socket, plus a counting
// socket named by MARVEL_SOCKET, as on every marvel seat. It returns the
// three counters: env, current cluster, other cluster.
func envClusterFixture(t *testing.T) (env, current, other *atomic.Int32) {
	t.Helper()
	// A short directory keeps the unix socket paths under the platform limit.
	home, err := os.MkdirTemp("", "mv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	oldSocket, oldCluster := socketPath, clusterName
	socketPath, clusterName = "", ""
	t.Cleanup(func() { socketPath, clusterName = oldSocket, oldCluster })

	envSock, env := countingSocket(t, home, "env.sock")
	curSock, current := countingSocket(t, home, "cur.sock")
	othSock, other := countingSocket(t, home, "oth.sock")
	t.Setenv(config.SocketEnv, envSock)
	cfg := &config.Config{
		Clusters: []config.Cluster{
			{Name: "current", Socket: curSock},
			{Name: "other", Socket: othSock},
		},
		CurrentCluster: "current",
	}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return env, current, other
}

// An explicit --cluster outranks MARVEL_SOCKET: the command reaches the
// named cluster, not the daemon of the seat it was typed in (#586).
func TestExplicitClusterBeatsEnvSocket(t *testing.T) {
	env, current, other := envClusterFixture(t)
	clusterName = "other"

	_, _ = send(daemon.Request{Method: "status"})
	if n := other.Load(); n == 0 {
		t.Error("the named cluster was never dialed, want it reached")
	}
	if n := env.Load(); n != 0 {
		t.Errorf("MARVEL_SOCKET was dialed %d time(s), want 0", n)
	}
	if n := current.Load(); n != 0 {
		t.Errorf("the current cluster was dialed %d time(s), want 0", n)
	}
}

// A --cluster typo with MARVEL_SOCKET set refuses with the unknown-cluster
// error and dials nothing, instead of running on the env daemon (#586, #618).
func TestExplicitClusterTypoBesideEnvSocketRefuses(t *testing.T) {
	env, current, other := envClusterFixture(t)
	clusterName = "othr"

	_, err := send(daemon.Request{Method: "status"})
	if err == nil {
		t.Fatal("send succeeded for an unknown cluster beside MARVEL_SOCKET, want a refusal")
	}
	if !strings.Contains(err.Error(), `unknown cluster "othr"`) {
		t.Errorf("error = %q, want it to name the unknown cluster", err)
	}
	if n := env.Load() + current.Load() + other.Load(); n != 0 {
		t.Errorf("%d dial(s) were made, want 0", n)
	}
}

// MARVEL_SOCKET alone, with no --cluster, is unchanged: it still wins over
// the config's current cluster.
func TestEnvSocketAloneStillWins(t *testing.T) {
	env, current, other := envClusterFixture(t)

	_, _ = send(daemon.Request{Method: "status"})
	if n := env.Load(); n == 0 {
		t.Error("MARVEL_SOCKET was never dialed, want it reached")
	}
	if n := current.Load() + other.Load(); n != 0 {
		t.Errorf("a configured cluster was dialed %d time(s), want 0", n)
	}
}
