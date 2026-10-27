package main

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// olderDaemon answers every method as a daemon without prune_plan does, and
// records the methods it was asked for.
func olderDaemon(t *testing.T) func() []string {
	t.Helper()
	home, err := os.MkdirTemp("", "mp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	oldSocket, oldCluster, oldGiven := socketPath, clusterName, clusterFlagGiven
	socketPath, clusterName, clusterFlagGiven = "", "", false
	t.Cleanup(func() { socketPath, clusterName, clusterFlagGiven = oldSocket, oldCluster, oldGiven })
	sock := filepath.Join(home, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var (
		mu      sync.Mutex
		methods []string
		wg      sync.WaitGroup
	)
	t.Cleanup(func() { _ = ln.Close(); wg.Wait() })
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			var req daemonrpc.Request
			resp := daemonrpc.Response{}
			if err := json.NewDecoder(c).Decode(&req); err == nil {
				mu.Lock()
				methods = append(methods, req.Method)
				mu.Unlock()
				resp.Error = "unknown method: " + req.Method
			}
			_ = json.NewEncoder(c).Encode(resp)
			_ = c.Close()
		}
	}()
	t.Setenv(config.SocketEnv, sock)
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), methods...)
	}
}

func writeManifest(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "marvel.toml")
	data := "[workspace]\nname = \"ws\"\n\n[[team]]\nname = \"squad\"\n\n  [[team.role]]\n  name = \"worker\"\n  replicas = 0\n\n    [team.role.runtime]\n    command = \"bash\"\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func runPlan(args ...string) error {
	cmd := planCmd()
	cmd.SetArgs(args)
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	return cmd.Execute()
}

// A daemon without prune_plan answers "unknown method". The preview must fail
// closed: exit 1, and never fall back to an apply or the convergence plan.
func TestPlanPruneFailsClosedOnAnOlderDaemon(t *testing.T) {
	asked := olderDaemon(t)
	err := runPlan("-f", writeManifest(t), "--prune")
	var ee *exitError
	if !errors.As(err, &ee) || ee.Code != 1 {
		t.Fatalf("plan --prune against an older daemon = %v, want exit 1", err)
	}
	got := asked()
	if len(got) != 1 || got[0] != "prune_plan" {
		t.Errorf("methods sent = %q, want only prune_plan", got)
	}
}

// Until the full manifest plan lands, plan -f without --prune is refused with a
// pointer to it, and --prune needs -f. Neither reaches the daemon.
func TestPlanFileFlagsAreRefusedWithoutTheirPartner(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"file without prune", []string{"-f", "FILE"}, "aae-orc-qawkf"},
		{"prune without file", []string{"--prune"}, "-f"},
		{"file with a filter", []string{"-f", "FILE", "--prune", "ws/squad"}, "-f"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asked := olderDaemon(t)
			path := writeManifest(t)
			args := make([]string, len(tc.args))
			for i, a := range tc.args {
				args[i] = strings.ReplaceAll(a, "FILE", path)
			}
			err := runPlan(args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("plan %q = %v, want a refusal naming %q", args, err, tc.want)
			}
			if got := asked(); len(got) != 0 {
				t.Errorf("plan %q sent %q, want nothing sent", args, got)
			}
		})
	}
}
