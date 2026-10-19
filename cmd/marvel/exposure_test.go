package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

type seenRequest struct {
	Method string
	Params map[string]any
}

// exposureFakeDaemon answers every exposure.* request with an empty result and
// records what it was sent.
func exposureFakeDaemon(t *testing.T) func() []seenRequest {
	t.Helper()
	dir, err := os.MkdirTemp("", "ex")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	t.Setenv(config.SocketEnv, socket)
	var (
		mu   sync.Mutex
		seen []seenRequest
	)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			var req daemonrpc.Request
			if err := json.NewDecoder(c).Decode(&req); err == nil {
				var p map[string]any
				_ = json.Unmarshal(req.Params, &p)
				mu.Lock()
				seen = append(seen, seenRequest{Method: req.Method, Params: p})
				mu.Unlock()
				out, _ := json.Marshal(map[string]any{"count": 1})
				_ = json.NewEncoder(c).Encode(daemonrpc.Response{Result: out})
			}
			_ = c.Close()
		}
	}()
	return func() []seenRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]seenRequest(nil), seen...)
	}
}

func runExposure(t *testing.T, args ...string) error {
	t.Helper()
	cmd := exposureCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	return cmd.Execute()
}

// TestExposureCmdSendsTheVerbs: add, end and void send exposure.add, exposure.end
// and exposure.void with the slot, the function and the flags.
func TestExposureCmdSendsTheVerbs(t *testing.T) {
	resolveFixture(t, false)
	seen := exposureFakeDaemon(t)

	if err := runExposure(t, "add", "ws/ops", "writer", "Parse", "--path", "src/a", "--path", "src/b", "--caster", "supervisor"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := runExposure(t, "end", "ws/ops", "writer", "parse"); err != nil {
		t.Fatalf("end: %v", err)
	}
	if err := runExposure(t, "void", "ws/ops", "writer", "parse", "--reason", "wrong role"); err != nil {
		t.Fatalf("void: %v", err)
	}
	got := seen()
	if len(got) != 3 || got[0].Method != "exposure.add" || got[1].Method != "exposure.end" || got[2].Method != "exposure.void" {
		t.Fatalf("requests = %+v, want add, end, void", got)
	}
	add := got[0].Params
	if add["workspace"] != "ws" || add["team"] != "ops" || add["role"] != "writer" || add["function"] != "Parse" || add["caster"] != "supervisor" {
		t.Errorf("add params = %+v", add)
	}
	if paths, _ := add["paths"].([]any); len(paths) != 2 {
		t.Errorf("add paths = %v, want two", add["paths"])
	}
	if got[2].Params["reason"] != "wrong role" {
		t.Errorf("void params = %+v, want the reason", got[2].Params)
	}
}

// TestExposureCmdRefusesBeforeSending: a slot that is not workspace/team, a name
// outside the class, and a void with no reason never reach the daemon.
func TestExposureCmdRefusesBeforeSending(t *testing.T) {
	resolveFixture(t, false)
	seen := exposureFakeDaemon(t)

	cases := [][]string{
		{"add", "ops", "writer", "parse"},
		{"add", "ws/ops", "writer", "par se"},
		{"end", "ws/ops", "writer", "pa\u200brse"},
		{"void", "ws/ops", "writer", "parse"},
		{"void", "ws/ops", "writer", "parse", "--reason", "  "},
	}
	for _, args := range cases {
		if err := runExposure(t, args...); err == nil {
			t.Errorf("%s: accepted, want an error", strings.Join(args, " "))
		}
	}
	if got := seen(); len(got) != 0 {
		t.Errorf("%d requests reached the daemon, want none: %+v", len(got), got)
	}
}
