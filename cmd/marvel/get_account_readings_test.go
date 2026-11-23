package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// readingsFakeDaemon answers `get` with the given samples and records the get
// params it was sent.
func readingsFakeDaemon(t *testing.T, samples []api.AccountSample) (socket string, params func() []daemonrpc.GetParams) {
	t.Helper()
	dir, err := os.MkdirTemp("", "ar")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket = filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var (
		mu  sync.Mutex
		got []daemonrpc.GetParams
	)
	result, _ := json.Marshal(samples)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			var req daemonrpc.Request
			if err := json.NewDecoder(c).Decode(&req); err == nil && req.Method == "get" {
				var p daemonrpc.GetParams
				_ = json.Unmarshal(req.Params, &p)
				mu.Lock()
				got = append(got, p)
				mu.Unlock()
				_ = json.NewEncoder(c).Encode(daemonrpc.Response{Result: result})
			}
			_ = c.Close()
		}
	}()
	return socket, func() []daemonrpc.GetParams {
		mu.Lock()
		defer mu.Unlock()
		return append([]daemonrpc.GetParams(nil), got...)
	}
}

// The verb sends its two filters to the daemon and prints one row per reading,
// with the reset time in UTC.
func TestGetAccountReadingsSendsTheFiltersAndPrintsTheRows(t *testing.T) {
	at := time.Date(2026, 11, 23, 4, 0, 0, 0, time.UTC)
	sock, params := readingsFakeDaemon(t, []api.AccountSample{{
		Cluster: "k", Account: "claude default - default-home", Window: "seven_day",
		UsedPercent: 42.5, ResetsAt: at.Add(48 * time.Hour), At: at, Session: "ws/seat",
	}})
	code, out, errOut := runFanGet(t, "--socket", sock, "get", "account-readings", "--account", "claude", "--since", "1h")
	if code != 0 {
		t.Fatalf("exit %d, want 0; stderr %q", code, errOut)
	}
	got := params()
	if len(got) != 1 || got[0].ResourceType != "account-readings" || got[0].Account != "claude" || got[0].SinceSeconds != 3600 {
		t.Fatalf("daemon was sent %+v, want one account-readings get with account claude and since_seconds 3600", got)
	}
	for _, want := range []string{"ACCOUNT", "seven_day", "42.5", "2026-11-25T04:00:00Z", "2026-11-23T04:00:00Z", "ws/seat"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
}

// -o json prints the daemon's list as a document, and no readings is a line
// saying so rather than an empty table.
func TestGetAccountReadingsJSONAndEmpty(t *testing.T) {
	sock, _ := readingsFakeDaemon(t, []api.AccountSample{{Account: "a", Window: "five_hour", UsedPercent: 1, At: time.Unix(0, 0).UTC()}})
	code, out, errOut := runFanGet(t, "--socket", sock, "get", "account-readings", "-o", "json")
	if code != 0 {
		t.Fatalf("exit %d; stderr %q", code, errOut)
	}
	var got []api.AccountSample
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got) != 1 || got[0].Window != "five_hour" {
		t.Fatalf("-o json = %q (%v), want the one sample as a list", out, err)
	}
	empty, _ := readingsFakeDaemon(t, nil)
	_, out, _ = runFanGet(t, "--socket", empty, "get", "account-readings")
	if !strings.Contains(out, "no account readings") {
		t.Errorf("empty list printed %q, want a line saying there are none", out)
	}
}

// Refusals come before any dial, and --account and --since mean nothing for
// another resource.
func TestGetAccountReadingsFlagRules(t *testing.T) {
	sock, params := readingsFakeDaemon(t, nil)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"account on sessions", []string{"get", "sessions", "--account", "x"}, "--account and --since apply to get account-readings only"},
		{"since on budgets", []string{"get", "budgets", "--since", "1h"}, "--account and --since apply to get account-readings only"},
		{"yaml", []string{"get", "account-readings", "-o", "yaml"}, "-o yaml is not supported"},
		{"negative since", []string{"get", "account-readings", "--since=-1h"}, "--since must not be negative"},
		{"watch", []string{"get", "account-readings", "--watch"}, "does not support --watch"},
		{"columns", []string{"get", "account-readings", "--columns", "key"}, "--columns applies to get sessions only"},
	}
	for _, c := range cases {
		code, _, stderr := runFanGet(t, append([]string{"--socket", sock}, c.args...)...)
		if code != 1 {
			t.Errorf("%s: exit = %d, want 1\n%s", c.name, code, stderr)
		}
		if !strings.Contains(stderr, c.want) {
			t.Errorf("%s: stderr lacks %q:\n%s", c.name, c.want, stderr)
		}
	}
	if n := len(params()); n != 0 {
		t.Errorf("%d reads reached the daemon from refused commands, want 0", n)
	}
}
