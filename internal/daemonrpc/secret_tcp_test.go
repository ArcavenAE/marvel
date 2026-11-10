package daemonrpc

import (
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/arcavenae/marvel/internal/shorttmp"
)

// A secret-bearing request is sent only over a unix socket or an authenticated
// tunnel (mrvl:// or ssh://). Over bare TCP the bytes would cross the network
// in the clear, so the client refuses before it dials.

func TestIsBareTCP(t *testing.T) {
	t.Parallel()
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:6785", true},
		{":9090", true},
		{"tcp://10.0.0.1:6785", true},
		{"/run/marvel.sock", false},
		{"mrvl://host", false},
		{"mrvl://host:6785", false},
		{"ssh://op@host/run/marvel.sock", false},
		{"ssh://host:9090", false},
	}
	for _, tc := range cases {
		if got := IsBareTCP(tc.addr); got != tc.want {
			t.Errorf("IsBareTCP(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}

func countingTCP(t *testing.T) (addr string, hits *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	hits = new(atomic.Int32)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			hits.Add(1)
			_ = c.Close()
		}
	}()
	return ln.Addr().String(), hits
}

func TestSecretBearingRequestIsNotSentOverBareTCP(t *testing.T) {
	addr, hits := countingTCP(t)
	for _, method := range []string{
		"credential.put", "credential.reveal", "credential.get", "broker.token", "handoff.put",
		"heartbeat", "bus.beat", "account.limits", "account.series", "account.delta",
	} {
		for _, a := range []string{addr, "tcp://" + addr} {
			_, err := SendRequest(a, Request{Method: method, Params: json.RawMessage(`{"value":"x"}`)})
			if !errors.Is(err, ErrSecretOverBareTCP) {
				t.Errorf("%s over %s: err = %v, want ErrSecretOverBareTCP", method, a, err)
			}
		}
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("the client connected %d times for a secret-bearing request, want 0", n)
	}
}

// Control: the refusal is about secret-bearing methods only; an ordinary
// method still dials whatever address it is given.
func TestOrdinaryRequestStillDialsTCP(t *testing.T) {
	addr, hits := countingTCP(t)
	_, _ = SendRequest(addr, Request{Method: "get", Params: json.RawMessage(`{}`)})
	if n := hits.Load(); n != 1 {
		t.Errorf("an ordinary request connected %d times, want 1", n)
	}
}

// Control: a secret-bearing request over a unix socket goes through.
func TestSecretBearingRequestStillGoesOverUnixSocket(t *testing.T) {
	// A short directory: t.TempDir on macOS can push the socket path past the
	// 104-byte limit, and a control that skips there proves nothing.
	sock := filepath.Join(shorttmp.Dir(t), "m.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("cannot listen on a unix socket at %s (%d bytes): %v", sock, len(sock), err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var hits atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			hits.Add(1)
			_ = c.Close()
		}
	}()
	_, _ = SendRequest(sock, Request{Method: "credential.put", Params: json.RawMessage(`{"value":"x"}`)})
	if n := hits.Load(); n != 1 {
		t.Errorf("a secret-bearing request over a unix socket connected %d times, want 1", n)
	}
}
