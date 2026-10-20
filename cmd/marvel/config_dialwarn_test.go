package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/config"
)

// runAddCluster runs `marvel config add-cluster` in a scratch home and returns
// what it wrote to stderr.
func runAddCluster(t *testing.T, args ...string) (string, error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MARVEL_SOCKET", "")
	cmd := configCmd()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetArgs(append([]string{"add-cluster"}, args...))
	err := cmd.Execute()
	return errb.String(), err
}

func TestAddClusterWarnsOnAnIPv4LiteralAndStillWrites(t *testing.T) {
	stderr, err := runAddCluster(t, "x", "mrvl://192.0.2.7:6785")
	if err != nil {
		t.Fatalf("add-cluster refused a literal: %v", err)
	}
	if !strings.Contains(stderr, "192.0.2.7") || !strings.Contains(stderr, "warning") {
		t.Errorf("no literal warning on stderr: %q", stderr)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cl, err := cfg.GetCluster("x")
	if err != nil || cl == nil || cl.Server != "mrvl://192.0.2.7:6785" {
		t.Fatalf("the cluster was not written: %+v, %v", cl, err)
	}
}

func TestAddClusterIsQuietForANameAndForASocket(t *testing.T) {
	for _, addr := range []string{"mrvl://kinu:6785", "/tmp/marvel-dev.sock"} {
		stderr, err := runAddCluster(t, "x", addr)
		if err != nil {
			t.Fatalf("%s: %v", addr, err)
		}
		if stderr != "" {
			t.Errorf("%s: unexpected stderr %q", addr, stderr)
		}
	}
}
