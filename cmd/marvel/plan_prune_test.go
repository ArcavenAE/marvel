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
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// olderDaemon answers every method as a daemon without prune_plan does, and
// records the methods it was asked for.
func olderDaemon(t *testing.T) func() []string {
	t.Helper()
	return pruneFakeDaemon(t, nil)
}

// pruneFakeDaemon answers prune_plan with plan, or every method with "unknown
// method" when plan is nil, and records the methods it was asked for.
func pruneFakeDaemon(t *testing.T, plan *api.PrunePlan) func() []string {
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
				if plan != nil && req.Method == "prune_plan" {
					resp.Result, _ = json.Marshal(plan)
				} else {
					resp.Error = "unknown method: " + req.Method
				}
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
	_, err := runPlanOut(args...)
	return err
}

func runPlanOut(args ...string) (string, error) {
	out := &strings.Builder{}
	cmd := planCmd()
	cmd.SetArgs(args)
	cmd.SetOut(out)
	cmd.SetErr(&strings.Builder{})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.Execute()
	return out.String(), err
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

// The preview prints each group of section 4 and the line that
// says apply's other pre-flights were not evaluated (the step 2 ruling, Q6). It
// exits 0, and 2 under --detailed-exitcode when something would be deleted.
func TestPlanPrunePrintsTheGroupsAndExitsByWhatItFound(t *testing.T) {
	at := time.Date(2026, 10, 27, 18, 2, 0, 0, time.UTC)
	plan := &api.PrunePlan{
		Workspace: "ws",
		Source:    "host-a:/srv/manifests/main.toml",
		WouldDelete: []api.PruneDelete{
			{Team: "aae/old-research", Seats: []api.PruneSeat{{Session: "aae/old-research-worker-g1-0", State: api.SessionFailed}}, Handoff: "handoff: declared, window 5m0s"},
			{Team: "aae/ops-trial"},
		},
		KeptShared:    []api.PruneShared{{Team: "aae/shared-ops", Others: map[string]time.Time{"host-a:/srv/manifests/ops.toml": at}}},
		KeptUnclaimed: []string{"aae/fossil-a"},
		KeptRun:       []api.PruneRun{{Team: "aae/scratch", Runs: 1}},
		Deferred:      []api.PruneDeferred{{Team: "aae/night-batch", Reason: "shift in progress; re-run the plan when it ends"}},
		Held:          []api.PruneHeld{{Team: "aae/builders", WillSpawn: 2}},
	}
	pruneFakeDaemon(t, plan)
	path := writeManifest(t)
	out, err := runPlanOut("-f", path, "--prune")
	if err != nil {
		t.Fatalf("plan --prune = %v, want exit 0", err)
	}
	for _, want := range []string{
		"prune plan for workspace/ws from host-a:/srv/manifests/main.toml",
		"pre-flight checks not evaluated (aae-orc-qawkf)",
		"would delete (claimed only by this file, which no longer names them):",
		"aae/old-research   1 seat  1 failed   handoff: declared, window 5m0s",
		"aae/ops-trial   0 seats",
		"kept, claimed by another file too:",
		"aae/shared-ops   also host-a:/srv/manifests/ops.toml (applied 2026-10-27T18:02Z)",
		"kept, unclaimed (applied before claims existed, or by an older client):",
		"aae/fossil-a   delete by hand: marvel delete team aae/fossil-a",
		"kept, holds marvel run sessions:",
		"aae/scratch   1 run session (not the manifest's to remove)",
		"deferred:",
		"aae/night-batch   shift in progress; re-run the plan when it ends",
		"named by this file and held (re-apply converges them):",
		"aae/builders   will spawn 2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	_, err = runPlanOut("-f", path, "--prune", "--detailed-exitcode")
	var ee *exitError
	if !errors.As(err, &ee) || ee.Code != 2 {
		t.Errorf("--detailed-exitcode with teams to delete = %v, want exit 2", err)
	}
	plan.WouldDelete = nil
	if _, err := runPlanOut("-f", path, "--prune", "--detailed-exitcode"); err != nil {
		t.Errorf("--detailed-exitcode with nothing to delete = %v, want exit 0", err)
	}
}
