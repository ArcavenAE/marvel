package main

import (
	"testing"

	"github.com/arcavenae/marvel/internal/config"
)

// clusterArgFixture is resolveFixture with two named clusters, a and b,
// and every package-level input the single-cluster resolver reads set to a
// value no cluster entry holds: MARVEL_SOCKET, --socket, -i and
// current_cluster.
func clusterArgFixture(t *testing.T) *config.Config {
	t.Helper()
	resolveFixture(t, false)
	oldID, oldGiven := identityPath, clusterFlagGiven
	t.Cleanup(func() { identityPath, clusterFlagGiven = oldID, oldGiven })

	cfg := &config.Config{
		Clusters: []config.Cluster{
			{Name: "a", Socket: "/scratch/a.sock"},
			{Name: "b", Server: "mrvl://u@127.0.0.1:1", Identity: "/scratch/b_ed25519"},
			{Name: "c"},
		},
		CurrentCluster: "b",
	}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	t.Setenv(config.SocketEnv, "/scratch/env.sock")
	socketPath, identityPath, clusterName, clusterFlagGiven = "/scratch/flag.sock", "/scratch/flag_ed25519", "b", true
	return cfg
}

// TestResolveTakesClusterArg: two clusters resolve side by side from their
// own entries, and MARVEL_SOCKET, --socket, --cluster, -i and
// current_cluster reach neither.
func TestResolveTakesClusterArg(t *testing.T) {
	cfg := clusterArgFixture(t)
	for _, c := range []struct {
		name, addr, identity string
		rung                 resolveRung
	}{
		{"a", "/scratch/a.sock", "", rungCluster},
		{"b", "mrvl://u@127.0.0.1:1", "/scratch/b_ed25519", rungCluster},
		{"c", config.DefaultSocket(), "", rungDefault},
	} {
		got, err := resolveCluster(cfg, c.name)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got.Name != c.name || got.Addr != c.addr || got.Opts.Identity != c.identity || got.Rung != c.rung {
			t.Errorf("%s: got %+v, want name %q, addr %q, identity %q, rung %q", c.name, got, c.name, c.addr, c.identity, c.rung)
		}
	}
}

// TestResolveClusterRefusesEmptyAndUnknown: an empty name is refused, so
// current_cluster cannot stand in for it, and so is a name with no entry.
func TestResolveClusterRefusesEmptyAndUnknown(t *testing.T) {
	cfg := clusterArgFixture(t)
	for _, name := range []string{"", "nosuch"} {
		if got, err := resolveCluster(cfg, name); err == nil {
			t.Errorf("resolveCluster(%q) = %+v, want a refusal", name, got)
		}
	}
}

// TestResolveIdentityFromEntryOnly: the package identityPath, set by -i,
// never reaches a named cluster, whether or not the entry names a key.
func TestResolveIdentityFromEntryOnly(t *testing.T) {
	cfg := clusterArgFixture(t)
	for _, name := range []string{"a", "b", "c"} {
		got, err := resolveCluster(cfg, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.Opts.Identity == identityPath {
			t.Errorf("%s: identity = %q, the -i value", name, got.Opts.Identity)
		}
	}
}

// TestSingleClusterResolveUnchanged is the control: the single-cluster
// resolver still lets -i override the entry's identity.
func TestSingleClusterResolveUnchanged(t *testing.T) {
	clusterArgFixture(t)
	socketPath = ""
	addr, opts, rung, err := resolveDaemonRung()
	if err != nil || addr != "mrvl://u@127.0.0.1:1" || opts.Identity != "/scratch/flag_ed25519" || rung != rungCluster {
		t.Errorf("got (%q, %q, %q, %v), want cluster b's address with the -i identity on rung %q", addr, opts.Identity, rung, err, rungCluster)
	}
}
