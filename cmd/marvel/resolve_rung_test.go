package main

import (
	"testing"

	"github.com/arcavenae/marvel/internal/config"
)

// resolveFixture isolates the client from the live fleet: a scratch home
// with one named cluster, no MARVEL_SOCKET, and the --socket and --cluster
// flag variables restored afterwards.
func resolveFixture(t *testing.T, withCluster bool) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv(config.SocketEnv, "")
	oldSocket, oldCluster := socketPath, clusterName
	socketPath, clusterName = "", ""
	t.Cleanup(func() { socketPath, clusterName = oldSocket, oldCluster })

	if !withCluster {
		return
	}
	cfg := &config.Config{
		Clusters:       []config.Cluster{{Name: "testcluster", Socket: "/scratch/cluster.sock"}},
		CurrentCluster: "testcluster",
	}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// A MARVEL_SOCKET in the environment wins over the selected cluster, and
// the rung says so. This is the order #586 made hard to see.
func TestResolveRungEnvBeatsCluster(t *testing.T) {
	resolveFixture(t, true)
	t.Setenv(config.SocketEnv, "/scratch/env.sock")

	addr, _, rung, _ := resolveDaemonRung()
	if addr != "/scratch/env.sock" {
		t.Errorf("addr = %q, want the env socket", addr)
	}
	if rung != rungEnv {
		t.Errorf("rung = %q, want %q", rung, rungEnv)
	}
}

// Each rung names itself: flag, env, cluster, and default.
func TestResolveRungNamesEachRung(t *testing.T) {
	t.Run("flag", func(t *testing.T) {
		resolveFixture(t, true)
		t.Setenv(config.SocketEnv, "/scratch/env.sock")
		socketPath = "/scratch/flag.sock"
		addr, _, rung, _ := resolveDaemonRung()
		if addr != "/scratch/flag.sock" || rung != rungFlag {
			t.Errorf("got (%q, %q), want the flag socket on rung %q", addr, rung, rungFlag)
		}
	})
	t.Run("cluster", func(t *testing.T) {
		resolveFixture(t, true)
		addr, _, rung, _ := resolveDaemonRung()
		if addr != "/scratch/cluster.sock" || rung != rungCluster {
			t.Errorf("got (%q, %q), want the cluster socket on rung %q", addr, rung, rungCluster)
		}
	})
	t.Run("default", func(t *testing.T) {
		resolveFixture(t, false)
		addr, _, rung, _ := resolveDaemonRung()
		if addr != config.ResolveSocket() || rung != rungDefault {
			t.Errorf("got (%q, %q), want the default socket on rung %q", addr, rung, rungDefault)
		}
	})
}

// resolveDaemonAddr keeps its two results: a caller that does not care
// about the rung sees no change.
func TestResolveDaemonAddrMatchesRungAddr(t *testing.T) {
	resolveFixture(t, true)
	t.Setenv(config.SocketEnv, "/scratch/env.sock")

	want, _, _, _ := resolveDaemonRung()
	if got, _, _ := resolveDaemonAddr(); got != want {
		t.Errorf("resolveDaemonAddr = %q, want %q", got, want)
	}
}
