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

	"github.com/arcavenae/marvel/internal/bus"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemon"
)

// describeFakeDaemon answers daemon.status with the given record, anything
// else with an error, and records the methods it was sent. The socket lives
// in a short directory: a unix socket path is capped near 104 bytes.
func describeFakeDaemon(t *testing.T, st daemon.DaemonStatus) (socket string, methods func() []string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "dd")
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
		got []string
	)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			var req daemon.Request
			if err := json.NewDecoder(c).Decode(&req); err == nil {
				mu.Lock()
				got = append(got, req.Method)
				mu.Unlock()
				if req.Method == "daemon.status" {
					data, _ := json.Marshal(st)
					_ = json.NewEncoder(c).Encode(daemon.Response{Result: data})
				} else {
					_ = json.NewEncoder(c).Encode(daemon.Response{Error: "unexpected method " + req.Method})
				}
			}
			_ = c.Close()
		}
	}()
	return socket, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), got...)
	}
}

func cannedStatus() daemon.DaemonStatus {
	return daemon.DaemonStatus{
		Home:    "/scratch/home",
		Cluster: "alpha",
		MRVL:    daemon.MRVLStatus{State: daemon.MRVLNetwork, Addr: "[::]:6785"},
		Bus:     &bus.Status{Managed: true, Leaf: "up", LeafFor: "2h9m"},
	}
}

// describe daemon carries the bind and the rung together: the rung is the
// client's own fact (which step chose the address), the bind is the
// daemon's, and one record shows both, so a wrong-daemon read is visible
// as one.
func TestDescribeDaemonCarriesBindAndRung(t *testing.T) {
	resolveFixture(t, false)
	socket, _ := describeFakeDaemon(t, cannedStatus())
	t.Setenv(config.SocketEnv, socket)

	var out bytes.Buffer
	if err := describeDaemon(&out); err != nil {
		t.Fatalf("describeDaemon: %v", err)
	}
	var got struct {
		Address string `json:"address"`
		Rung    string `json:"rung"`
		Cluster string `json:"cluster"`
		MRVL    struct {
			State string `json:"state"`
			Addr  string `json:"addr"`
		} `json:"mrvl"`
		Bus struct {
			Leaf    string `json:"leaf"`
			LeafFor string `json:"leaf_for"`
		} `json:"bus"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if got.Address != socket || got.Rung != string(rungEnv) {
		t.Errorf("address=%q rung=%q, want %q on rung %q", got.Address, got.Rung, socket, rungEnv)
	}
	if got.MRVL.State != "network" || got.MRVL.Addr != "[::]:6785" {
		t.Errorf("mrvl = %+v, want network [::]:6785", got.MRVL)
	}
	if got.Cluster != "alpha" || got.Bus.Leaf != "up" || got.Bus.LeafFor != "2h9m" {
		t.Errorf("cluster=%q bus=%+v, want the daemon's own record", got.Cluster, got.Bus)
	}
}

// The record comes from one daemon.status call, not a daemon.status call
// plus a separate bus.status, so the header and describe agree.
func TestDescribeDaemonMakesOneStatusCall(t *testing.T) {
	resolveFixture(t, false)
	socket, methods := describeFakeDaemon(t, cannedStatus())
	t.Setenv(config.SocketEnv, socket)

	if err := describeDaemon(&bytes.Buffer{}); err != nil {
		t.Fatalf("describeDaemon: %v", err)
	}
	if got := methods(); len(got) != 1 || got[0] != "daemon.status" {
		t.Errorf("methods = %v, want exactly [daemon.status]", got)
	}
}

// The rung is the one that chose the address, not a constant: a --socket
// flag reads as the flag rung.
func TestDescribeDaemonNamesTheFlagRung(t *testing.T) {
	resolveFixture(t, false)
	socket, _ := describeFakeDaemon(t, cannedStatus())
	socketPath = socket

	var out bytes.Buffer
	if err := describeDaemon(&out); err != nil {
		t.Fatalf("describeDaemon: %v", err)
	}
	var got struct {
		Rung string `json:"rung"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Rung != string(rungFlag) {
		t.Errorf("rung = %q (err %v), want %q", got.Rung, err, rungFlag)
	}
}

// describe daemon takes no name, and every other resource still takes one.
func TestDescribeDaemonTakesNoName(t *testing.T) {
	cmd := describeCmd()
	if err := cmd.Args(cmd, []string{"daemon"}); err != nil {
		t.Errorf("describe daemon: %v, want it accepted", err)
	}
	if err := cmd.Args(cmd, []string{"daemon", "x"}); err == nil || !strings.Contains(err.Error(), "daemon") {
		t.Errorf("describe daemon x: err = %v, want a refusal that names daemon", err)
	}
	if err := cmd.Args(cmd, []string{"session"}); err == nil {
		t.Error("describe session with no name was accepted")
	}
	if err := cmd.Args(cmd, []string{"session", "ws/agent-0"}); err != nil {
		t.Errorf("describe session ws/agent-0: %v, want it accepted", err)
	}
}

// A daemon that answers with an error is an error, not an empty record.
func TestDescribeDaemonReportsADaemonError(t *testing.T) {
	resolveFixture(t, false)
	dir, err := os.MkdirTemp("", "dd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			var req daemon.Request
			_ = json.NewDecoder(c).Decode(&req)
			_ = json.NewEncoder(c).Encode(daemon.Response{Error: "boom"})
			_ = c.Close()
		}
	}()
	t.Setenv(config.SocketEnv, socket)

	var out bytes.Buffer
	if err := describeDaemon(&out); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want the daemon's error", err)
	}
}

// The command itself routes `describe daemon` to the record, through the
// command's own output stream.
func TestDescribeCmdRoutesDaemon(t *testing.T) {
	resolveFixture(t, false)
	socket, methods := describeFakeDaemon(t, cannedStatus())
	t.Setenv(config.SocketEnv, socket)

	cmd := describeCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"daemon"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("describe daemon: %v", err)
	}
	if !strings.Contains(out.String(), `"rung": "env"`) {
		t.Errorf("describe daemon printed %q, want the record with its rung", out.String())
	}
	if got := methods(); len(got) != 1 || got[0] != "daemon.status" {
		t.Errorf("methods = %v, want exactly [daemon.status]", got)
	}
}
