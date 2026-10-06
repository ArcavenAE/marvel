package bus

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// dropFirst fronts a broker address with a listener that closes the first
// connections it accepts without a byte, then relays the rest. It stands in
// for a broker that drops a fresh connection the moment it is dialed.
func dropFirst(t *testing.T, target string, drops int32) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var seen atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			if seen.Add(1) <= drops {
				if tc, ok := c.(*net.TCPConn); ok {
					_ = tc.SetLinger(0) // reset, not an orderly close
				}
				_ = c.Close()
				continue
			}
			go func() {
				up, err := net.Dial("tcp", target)
				if err != nil {
					_ = c.Close()
					return
				}
				go func() { _, _ = io.Copy(up, c); _ = up.Close() }()
				_, _ = io.Copy(c, up)
				_ = c.Close()
			}()
		}
	}()
	return "nats://" + ln.Addr().String()
}

// A broker that drops the first connection must not fail the provisioning
// pass: connectAdmin retries a transport error inside the same window it
// already uses for an authorization refusal (marvel#657).
func TestProvisionSurvivesADroppedFirstConnection(t *testing.T) {
	s, m, _ := newTestSupervisor(t, "")
	s.AfterReady = nil
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	admin := m.Admin()
	url := dropFirst(t, m.bus.Listen, 1)

	got, err := Provision(context.Background(), url, admin.Name, admin.Password)
	if err != nil {
		t.Fatalf("provision through a broker that dropped the first connection: %v", err)
	}
	if len(got.Created) != 3 {
		t.Errorf("created %v, want three objects on a fresh broker", got.Created)
	}
}

// A refused address is not a transient reset: it fails at once instead of
// spending the retry window.
func TestProvisionFailsAtOnceOnARefusedAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	start := time.Now()
	if _, err := Provision(context.Background(), "nats://"+addr, "u", "p"); err == nil {
		t.Fatal("provision against a closed port succeeded")
	}
	if d := time.Since(start); d > authRetryWindow/2 {
		t.Errorf("a refused address took %s to fail, want it to fail at once", d)
	}
}

func TestRetryableConnect(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"authorization", nats.ErrAuthorization, true},
		{"reset", fmt.Errorf("read: %w", syscall.ECONNRESET), true},
		{"broken pipe", fmt.Errorf("write: %w", syscall.EPIPE), true},
		{"eof", io.EOF, true},
		{"unexpected eof", io.ErrUnexpectedEOF, true},
		{"refused", fmt.Errorf("dial: %w", syscall.ECONNREFUSED), false},
		{"no servers", nats.ErrNoServers, false},
		{"other", errors.New("boom"), false},
	} {
		if got := retryableConnect(tc.err); got != tc.want {
			t.Errorf("%s: retryableConnect = %v, want %v", tc.name, got, tc.want)
		}
	}
}
