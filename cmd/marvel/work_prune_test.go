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

// A malformed digest is refused before anything is sent (rulings Q5, Q7).
func TestWorkPruneRefusesAMalformedDigestBeforeSending(t *testing.T) {
	for _, digest := range []string{"", "ABCDEF01", "abc", "0123abcd0"} {
		t.Run(digest, func(t *testing.T) {
			asked := applyFakeDaemon(t, appliedPrefix+`}`)
			_, err := runWork(writeManifest(t), "--prune", digest)
			var ee *exitError
			if !errors.As(err, &ee) || ee.Code != 1 || !strings.Contains(ee.Msg, "nothing was applied") {
				t.Fatalf("work --prune %q = %v, want exit 1 saying nothing was applied", digest, err)
			}
			if got := asked(); len(got) != 0 {
				t.Errorf("sent %d request(s), want none", len(got))
			}
		})
	}
}

// The prune rides on apply as prune and prune_digest beside the source.
func TestWorkPruneSendsTheDigestWithTheApply(t *testing.T) {
	asked := applyFakeDaemon(t, appliedPrefix+`,"pruned":["ws/gone"],"prune_plan":{},"prune_digest_given":"0123abcd"}`)
	out, err := runWork(writeManifest(t), "--prune", "0123abcd")
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
	if p["prune"] != true || p["prune_digest"] != "0123abcd" || p["manifest_source"] == nil {
		t.Errorf("params = %v, want prune, prune_digest and manifest_source", p)
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
			_, err := runWork(writeManifest(t), "--prune", "0123abcd")
			var ee *exitError
			if !errors.As(err, &ee) || ee.Code != 1 || !strings.Contains(ee.Msg, "nothing was pruned") {
				t.Fatalf("work --prune = %v, want exit 1 saying nothing was pruned", err)
			}
		})
	}
}

// A refused prune prints the current set and both digests, and exits 1 with
// the apply standing (rulings Q6, Q7).
func TestWorkPruneReportsAMismatch(t *testing.T) {
	applyFakeDaemon(t, appliedPrefix+`,"pruned":[],"prune_digest_given":"ffffffff","prune_refused":"prune digest does not match the current prune set",`+
		`"prune_plan":{"Workspace":"ws","Source":"h:/m.toml","Digest":"0123abcd","WouldDelete":[{"Team":"ws/gone","Seats":[]}]}}`)
	out, err := runWork(writeManifest(t), "--prune", "ffffffff")
	var ee *exitError
	if !errors.As(err, &ee) || ee.Code != 1 || !strings.Contains(ee.Msg, "nothing was pruned") {
		t.Fatalf("work --prune = %v, want exit 1 saying nothing was pruned", err)
	}
	for _, want := range []string{"ffffffff", "0123abcd", "ws/gone"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q is missing %q", out, want)
		}
	}
}

// An empty set, or teams a re-check skipped, is still exit 0 (ruling Q7).
func TestWorkPruneExitsZeroForAnEmptyOrSkippedSet(t *testing.T) {
	applyFakeDaemon(t, appliedPrefix+`,"pruned":[],"prune_plan":{},"prune_digest_given":"0123abcd","prune_skipped":[{"Team":"ws/two","Reason":"deferred: live seat, needs step 4"}]}`)
	out, err := runWork(writeManifest(t), "--prune", "0123abcd")
	if err != nil {
		t.Fatalf("work --prune = %v, want exit 0", err)
	}
	if !strings.Contains(out, "pruned: nothing") || !strings.Contains(out, "ws/two") {
		t.Errorf("output %q, want nothing pruned and the skipped team", out)
	}
}
