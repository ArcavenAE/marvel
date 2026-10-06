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

// A config whose own current_cluster names nothing refuses too, with the
// same error and no dial. The operator's ruling covers both places an
// unknown cluster can come from (#618).
func TestUnknownCurrentClusterRefuses(t *testing.T) {
	dialed := unknownClusterFixture(t)
	cfg := &config.Config{
		Clusters:       []config.Cluster{{Name: "testcluster", Socket: "/scratch/cluster.sock"}},
		CurrentCluster: "gone",
	}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	_, err := send(daemon.Request{Method: "status"})
	if err == nil {
		t.Fatal("send succeeded with an unknown current_cluster, want a refusal")
	}
	if !strings.Contains(err.Error(), `unknown cluster "gone"`) {
		t.Errorf("error = %q, want it to name the unknown cluster", err)
	}
	if n := dialed.Load(); n != 0 {
		t.Errorf("the local daemon was dialed %d time(s), want 0", n)
	}
}

// The exception: a command that defines or configures a cluster may name
// one that is not in the config yet. These commands take the name as an
// argument and never resolve a daemon, so a --cluster that matches nothing
// does not stop them. add-cluster defines the cluster, and use-cluster
// repairs a current_cluster that names nothing.
func TestConfigCommandsAcceptAnUnknownClusterFlag(t *testing.T) {
	_ = unknownClusterFixture(t)
	clusterName = "not-yet-defined"

	run := func(args ...string) error {
		cmd := configCmd()
		cmd.SetArgs(args)
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		return cmd.Execute()
	}
	if err := run("add-cluster", "not-yet-defined", "/scratch/new.sock"); err != nil {
		t.Fatalf("add-cluster with an unknown --cluster: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cl, err := cfg.GetCluster("not-yet-defined"); err != nil || cl == nil {
		t.Fatalf("add-cluster did not define the cluster: %v", err)
	}

	cfg.CurrentCluster = "gone"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if err := run("use-cluster", "testcluster"); err != nil {
		t.Fatalf("use-cluster on a config with an unknown current_cluster: %v", err)
	}
}

// A --cluster name cannot be looked up when the client config does not parse,
// so it is refused too, and nothing is dialed. Without --cluster the same
// unreadable config still falls back to the default socket (see
// TestResolveRungNamesEachRung, unreadable config).
func TestUnreadableConfigWithClusterFlagRefuses(t *testing.T) {
	dialed := unknownClusterFixture(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".marvel", "config.yaml"), []byte("clusters: [unclosed"), 0o600); err != nil {
		t.Fatal(err)
	}
	clusterName = "nosuch"

	_, err = send(daemon.Request{Method: "status"})
	if err == nil {
		t.Fatal("send succeeded with --cluster and an unreadable config, want a refusal")
	}
	if !strings.Contains(err.Error(), `"nosuch"`) {
		t.Errorf("error = %q, want it to name the cluster", err)
	}
	if n := dialed.Load(); n != 0 {
		t.Errorf("the local daemon was dialed %d time(s), want 0", n)
	}
}
