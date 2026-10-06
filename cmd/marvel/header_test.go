package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/bus"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemon"
)

var headerNow = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func busAt(leaf string, observed time.Duration, valid time.Duration) *bus.Status {
	return &bus.Status{
		Managed: true, Leaf: leaf,
		LeafObservedAt: headerNow.Add(-observed).Format(time.RFC3339),
		LeafValidUntil: headerNow.Add(-observed + valid).Format(time.RFC3339),
	}
}

func info(st daemon.DaemonStatus) headerInfo {
	return headerInfo{Address: "/scratch/m.sock", Rung: rungCluster, ClientName: "alpha", Status: &st}
}

// The cluster has up to three names: the one the client asked for, the one
// the daemon's own config gives it, and the bus domain. When they differ the
// header prints each, because the 2026-10-04 mismatch was two tools naming
// one cluster differently; when they agree it prints the name once.
func TestHeaderShowsBothNamesWhenDomainDiffers(t *testing.T) {
	differ := renderHeader(info(daemon.DaemonStatus{
		Cluster: "alpha", Bus: &bus.Status{Managed: true, Domain: "beta", Leaf: "n/a"},
	}), headerNow)
	for _, want := range []string{"alpha", "beta", "bus domain"} {
		if !strings.Contains(differ, want) {
			t.Errorf("header with a differing domain should mention %q:\n%s", want, differ)
		}
	}

	same := renderHeader(info(daemon.DaemonStatus{
		Cluster: "alpha", Bus: &bus.Status{Managed: true, Domain: "alpha", Leaf: "n/a"},
	}), headerNow)
	if strings.Count(same, "alpha") != 1 || strings.Contains(same, "bus domain") {
		t.Errorf("header with one name should print it once and no domain:\n%s", same)
	}

	daemonDiffers := renderHeader(headerInfo{
		Address: "/scratch/m.sock", Rung: rungCluster, ClientName: "alpha",
		Status: &daemon.DaemonStatus{Cluster: "gamma"},
	}, headerNow)
	if !strings.Contains(daemonDiffers, "alpha") || !strings.Contains(daemonDiffers, "gamma") {
		t.Errorf("header where the client and daemon names differ should show both:\n%s", daemonDiffers)
	}
}

// The rung is the step that chose the address: a MARVEL_SOCKET in the
// environment beats a configured cluster, and the header says so.
func TestHeaderRungNamesSocketEnvOverCluster(t *testing.T) {
	resolveFixture(t, true)
	socket, _ := describeFakeDaemon(t, daemon.DaemonStatus{Cluster: "testcluster"})
	t.Setenv(config.SocketEnv, socket)

	h := collectHeader()
	if h.Rung != rungEnv || h.Address != socket {
		t.Fatalf("collected rung=%q address=%q, want %q on %q", h.Rung, h.Address, rungEnv, socket)
	}
	out := renderHeader(h, headerNow)
	if !strings.Contains(out, "rung: env") || strings.Contains(out, "rung: cluster") {
		t.Errorf("header should name the env rung and not the cluster rung:\n%s", out)
	}
}

// The mrvl:// reach is one of three words, and the bound address rides it.
func TestHeaderBindThreeValues(t *testing.T) {
	cases := []struct {
		name string
		st   daemon.MRVLStatus
		want string
	}{
		{"off", daemon.MRVLStatus{State: daemon.MRVLOff}, "mrvl://  off"},
		{"loopback", daemon.MRVLStatus{State: daemon.MRVLLoopback, Addr: "127.0.0.1:6785"}, "mrvl://  loopback 127.0.0.1:6785"},
		{"wildcard network", daemon.MRVLStatus{State: daemon.MRVLNetwork, Addr: "[::]:6785"}, "mrvl://  network :6785"},
		{"named network", daemon.MRVLStatus{State: daemon.MRVLNetwork, Addr: "10.0.0.5:6785"}, "mrvl://  network 10.0.0.5:6785"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := renderHeader(info(daemon.DaemonStatus{MRVL: tc.st}), headerNow)
			if !strings.Contains(out, tc.want) {
				t.Errorf("header should contain %q:\n%s", tc.want, out)
			}
		})
	}

	// A daemon that reports no bind at all leaves the indicator out, so a
	// guess is never shown as a fact.
	if out := renderHeader(info(daemon.DaemonStatus{}), headerNow); strings.Contains(out, "mrvl://") {
		t.Errorf("a daemon that reported no bind should print no mrvl:// line:\n%s", out)
	}
}

// The bus word is "link up", never "connected": a leafnode count is not
// proof the subjects are carried. An unknown reading prints as unknown, an
// expired one as "?", and neither is ever read as down.
func TestHeaderLinkUpWording(t *testing.T) {
	cases := []struct {
		name string
		bus  *bus.Status
		want []string
		not  []string
	}{
		{"fresh up", busAt("up", 12*time.Second, time.Minute), []string{"link up, 12s ago"}, []string{"connected", "down"}},
		{"fresh down", busAt("down", 40*time.Second, time.Minute), []string{"link down, 40s ago"}, []string{"connected"}},
		{"expired", busAt("up", 9*time.Minute, time.Minute), []string{"link ?"}, []string{"link up", "connected"}},
		{"unknown", &bus.Status{Managed: true, Leaf: "unknown"}, []string{"bus      unknown"}, []string{"down", "link up", "connected"}},
		{"no hub", &bus.Status{Managed: true, Leaf: "n/a"}, []string{"bus      n/a"}, []string{"connected", "link"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := renderHeader(info(daemon.DaemonStatus{Bus: tc.bus}), headerNow)
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("header should contain %q:\n%s", w, out)
				}
			}
			for _, n := range tc.not {
				if strings.Contains(out, n) {
					t.Errorf("header should not contain %q:\n%s", n, out)
				}
			}
		})
	}
}

// A daemon that cannot answer daemon.status (an older one) still gets the
// client's half of the header and the reason, and the listing is not refused.
func TestHeaderWithoutDaemonStatusStillNamesTheClientsFacts(t *testing.T) {
	out := renderHeader(headerInfo{
		Address: "/scratch/m.sock", Rung: rungDefault, StatusErr: "unknown method: daemon.status",
	}, headerNow)
	for _, want := range []string{"rung: default", "/scratch/m.sock", "unknown method: daemon.status"} {
		if !strings.Contains(out, want) {
			t.Errorf("header should contain %q:\n%s", want, out)
		}
	}
}

// captureStdout runs fn with os.Stdout redirected and returns what it wrote.
// Not parallel: it swaps os.Stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		var b bytes.Buffer
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	return <-done
}

func getSessionsOutput(t *testing.T, tty bool, args ...string) string {
	t.Helper()
	resolveFixture(t, false)
	socket := headerSocket(t, func(req daemon.Request) daemon.Response {
		switch req.Method {
		case "get":
			data, _ := json.Marshal(headerSessions())
			return daemon.Response{Result: data}
		case "daemon.status":
			data, _ := json.Marshal(daemon.DaemonStatus{Cluster: "alpha", MRVL: daemon.MRVLStatus{State: daemon.MRVLOff}})
			return daemon.Response{Result: data}
		}
		return daemon.Response{Error: "unexpected " + req.Method}
	})
	t.Setenv(config.SocketEnv, socket)
	old := stdoutIsTTY
	stdoutIsTTY = func() bool { return tty }
	t.Cleanup(func() { stdoutIsTTY = old })

	return captureStdout(t, func() {
		cmd := getCmd()
		cmd.SetArgs(append([]string{"sessions"}, args...))
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		if err := cmd.Execute(); err != nil {
			t.Errorf("get sessions %v: %v", args, err)
		}
	})
}

// On a terminal the header is on by default; piped, the output is the plain
// table it always was.
func TestHeaderOmittedWhenStdoutNotTTY(t *testing.T) {
	piped := getSessionsOutput(t, false)
	if strings.Contains(piped, "cluster") || strings.Contains(piped, "mrvl://") {
		t.Errorf("a piped run should print no header:\n%s", piped)
	}
	if !strings.HasPrefix(piped, "WORKSPACE") {
		t.Errorf("a piped run should start with the table header:\n%s", piped)
	}

	terminal := getSessionsOutput(t, true)
	if !strings.Contains(terminal, "cluster") || !strings.Contains(terminal, "mrvl://") {
		t.Errorf("a terminal run should print the header:\n%s", terminal)
	}
	if !strings.Contains(terminal, "WORKSPACE") {
		t.Errorf("a terminal run should still print the table:\n%s", terminal)
	}
}

// --header forces the header for a piped run.
func TestHeaderFlagForcesWhenPiped(t *testing.T) {
	out := getSessionsOutput(t, false, "--header")
	if !strings.Contains(out, "cluster") || !strings.Contains(out, "mrvl://") {
		t.Errorf("--header on a pipe should print the header:\n%s", out)
	}
	if idx, tbl := strings.Index(out, "mrvl://"), strings.Index(out, "WORKSPACE"); idx < 0 || tbl < idx {
		t.Errorf("the header should come before the table:\n%s", out)
	}
}

func headerSessions() []api.Session {
	return []api.Session{{
		Name: "agent-0", Workspace: "ws", Team: "squad", Role: "worker",
		State: api.SessionRunning, PaneID: "%3",
	}}
}

// headerSocket serves every request through handler on a short-path unix
// socket and returns its path.
func headerSocket(t *testing.T, handler func(daemon.Request) daemon.Response) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "hs")
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
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			var req daemon.Request
			if err := json.NewDecoder(c).Decode(&req); err == nil {
				_ = json.NewEncoder(c).Encode(handler(req))
			}
			_ = c.Close()
		}
	}()
	return socket
}

// An older daemon has no daemon.status. The listing still works, and the
// header says it could not get a status and why.
func TestGetSessionsSurvivesADaemonWithoutStatus(t *testing.T) {
	resolveFixture(t, false)
	socket := headerSocket(t, func(req daemon.Request) daemon.Response {
		if req.Method == "get" {
			data, _ := json.Marshal(headerSessions())
			return daemon.Response{Result: data}
		}
		return daemon.Response{Error: "unknown method: " + req.Method}
	})
	t.Setenv(config.SocketEnv, socket)

	out := captureStdout(t, func() {
		cmd := getCmd()
		cmd.SetArgs([]string{"sessions", "--header"})
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		if err := cmd.Execute(); err != nil {
			t.Errorf("get sessions --header against an older daemon: %v, want the listing", err)
		}
	})
	if !strings.Contains(out, "no status (unknown method: daemon.status)") || !strings.Contains(out, "agent-0") {
		t.Errorf("want the header's no-status line and the table:\n%s", out)
	}
}
