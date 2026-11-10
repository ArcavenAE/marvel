package main

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
)

// A TCP address is a network address, not the daemon's own unix socket. The
// reexec request sends this host's binary path only to a local daemon, so a
// bare host:port must read as remote.
func TestIsLocalDaemonAddr(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"/Users/op/.marvel/run/marvel.sock", true},
		{"", false},
		{"mrvl://desk", false},
		{"ssh://op@host/run/marvel.sock", false},
		{"tcp://10.0.0.1:6785", false},
		{"10.0.0.1:6785", false},
		{"localhost:6785", false},
	}
	for _, tc := range cases {
		if got := isLocalDaemonAddr(tc.addr); got != tc.want {
			t.Errorf("isLocalDaemonAddr(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}

// The handoff tool writes a file on the daemon host on a seat's behalf, so it
// goes only to a local socket. A TCP address is not one, and nothing is dialed.
func TestMCPHandoffRefusesATCPSocketEnv(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var hit atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			hit.Add(1)
			_ = c.Close()
		}
	}()
	for _, sock := range []string{ln.Addr().String(), "tcp://" + ln.Addr().String()} {
		run := runMCP(t, map[string]string{config.SocketEnv: sock, api.HeartbeatTokenEnv: mcpTestToken},
			time.Second, mcpInit+mcpCall(2, mcpTextArg(t, "x")))
		if resultOf(t, nthReply(t, run.lines(t), 1))["isError"] != true {
			t.Errorf("%s: want isError true", sock)
		}
	}
	if n := hit.Load(); n != 0 {
		t.Errorf("the handoff tool dialed a TCP address %d times, want 0", n)
	}
}
