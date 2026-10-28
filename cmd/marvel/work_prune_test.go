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

// applyFakeDaemon answers apply with result and records each request.
func applyFakeDaemon(t *testing.T, result string) func() []daemonrpc.Request {
	t.Helper()
	home, err := os.MkdirTemp("", "mw")
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
		mu   sync.Mutex
		reqs []daemonrpc.Request
		wg   sync.WaitGroup
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
				reqs = append(reqs, req)
				mu.Unlock()
				if req.Method == "apply" {
					resp.Result = json.RawMessage(result)
				} else {
					resp.Error = "unknown method: " + req.Method
				}
			}
			_ = json.NewEncoder(c).Encode(resp)
			_ = c.Close()
		}
	}()
	t.Setenv(config.SocketEnv, sock)
	return func() []daemonrpc.Request {
		mu.Lock()
		defer mu.Unlock()
		return append([]daemonrpc.Request(nil), reqs...)
	}
}

func runWork(args ...string) (string, error) {
	out := &strings.Builder{}
	cmd := workCmd()
	cmd.SetArgs(args)
	cmd.SetOut(out)
	cmd.SetErr(&strings.Builder{})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.Execute()
	return out.String(), err
}

const appliedPrefix = `{"status":"applied","workspace":"ws","advisories":[],"behind":0`

// --prune takes no value: it sends prune=true and no digest, and prints the
// set the apply pruned (operator ruling, 2026-10-28).
func TestWorkPruneTakesNoValue(t *testing.T) {
	asked := applyFakeDaemon(t, appliedPrefix+`,"pruned":["ws/gone"],"draining":[],"prune_plan":{}}`)
	out, err := runWork(writeManifest(t), "--prune")
	if err != nil {
		t.Fatalf("work --prune = %v", err)
	}
	if !strings.Contains(out, "pruned ws/gone") {
		t.Errorf("output %q, want the pruned team", out)
	}
	got := asked()
	if len(got) != 1 || got[0].Method != "apply" {
		t.Fatalf("requests = %+v, want one apply", got)
	}
	var p map[string]any
	if err := json.Unmarshal(got[0].Params, &p); err != nil {
		t.Fatal(err)
	}
	if _, ok := p["prune_digest"]; p["prune"] != true || ok || p["manifest_source"] == nil {
		t.Errorf("params = %v, want prune and manifest_source and no prune_digest", p)
	}
}

// Without --prune, work sends neither prune nor prune_digest, so a plain
// apply never prunes.
func TestWorkWithoutPruneSendsNoPrune(t *testing.T) {
	asked := applyFakeDaemon(t, appliedPrefix+`}`)
	if _, err := runWork(writeManifest(t)); err != nil {
		t.Fatalf("work = %v", err)
	}
	got := asked()
	if len(got) != 1 || got[0].Method != "apply" {
		t.Fatalf("requests = %+v, want one apply", got)
	}
	var p map[string]any
	if err := json.Unmarshal(got[0].Params, &p); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"prune", "prune_digest"} {
		if _, ok := p[k]; ok {
			t.Errorf("params carry %s: %v", k, p)
		}
	}
}

// An older daemon drops prune and answers with no pruned key, or a pruned that
// is null; either way the client says nothing was pruned and exits 1
// (section 5 item 4, ruling Q12).
func TestWorkPruneFailsWhenTheResponseHasNoPrunedArray(t *testing.T) {
	for _, tc := range []struct{ name, result string }{
		{"omitted", appliedPrefix + `}`},
		{"null", appliedPrefix + `,"pruned":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			applyFakeDaemon(t, tc.result)
			_, err := runWork(writeManifest(t), "--prune")
			var ee *exitError
			if !errors.As(err, &ee) || ee.Code != 1 || !strings.Contains(ee.Msg, "nothing was pruned") {
				t.Fatalf("work --prune = %v, want exit 1 saying nothing was pruned", err)
			}
		})
	}
}

// An empty set, or teams a re-check skipped, is still exit 0 (ruling Q7).
func TestWorkPruneExitsZeroForAnEmptyOrSkippedSet(t *testing.T) {
	applyFakeDaemon(t, appliedPrefix+`,"pruned":[],"prune_plan":{},"prune_skipped":[{"Team":"ws/two","Reason":"deferred: live headless seat, left alone by operator ruling 2026-10-28"}]}`)
	out, err := runWork(writeManifest(t), "--prune")
	if err != nil {
		t.Fatalf("work --prune = %v, want exit 0", err)
	}
	if !strings.Contains(out, "pruned: nothing") || !strings.Contains(out, "ws/two") {
		t.Errorf("output %q, want nothing pruned and the skipped team", out)
	}
}

// A step 4 daemon lists teams left to drain; the client prints them beside the
// pruned teams and exits 0. A step 3 daemon sends no draining key, which reads
// as none (step 4 ruling, Q6).
func TestWorkPrunePrintsDrainingTeams(t *testing.T) {
	applyFakeDaemon(t, appliedPrefix+`,"pruned":["ws/gone"],"draining":[{"Team":"ws/two","Seats":2}],"prune_plan":{}}`)
	out, err := runWork(writeManifest(t), "--prune")
	if err != nil {
		t.Fatalf("work --prune = %v, want exit 0", err)
	}
	if !strings.Contains(out, "pruned ws/gone") || !strings.Contains(out, "draining ws/two   2 seats") {
		t.Errorf("output %q, want the pruned team and the draining one", out)
	}
	applyFakeDaemon(t, appliedPrefix+`,"pruned":["ws/gone"],"prune_plan":{}}`)
	out, err = runWork(writeManifest(t), "--prune")
	if err != nil || strings.Contains(out, "draining") {
		t.Errorf("against a step 3 daemon: %q, %v; want no draining line and exit 0", out, err)
	}
}
