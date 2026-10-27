package bus

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"slices"
	"strconv"
	"sync"
	"testing"
)

// The package's tests must dial only a broker they started (marvel#998). A
// fixture address such as 127.0.0.1:4222 is the managed broker's own listen
// address, so on a host running marvel a dial there reaches the live broker,
// which logs the test's freshly minted password as an authentication failure.
// freePort marks each port it hands out as started; guardDial refuses any
// other address before it is dialed, and TestMain fails the run if it refused
// one. An empty URL is refused too: the client dials its default,
// nats://127.0.0.1:4222, for it. Many dials run on a background goroutine with no test to fail, which
// is why the run, not one test, carries the failure.

var errNotStarted = errors.New("not a broker this test started")

var dialGuardState = struct {
	sync.Mutex
	started map[string]bool
	refused []string
}{started: map[string]bool{}}

// markStarted records hostport as an address a test of this binary started a
// broker on, or will.
func markStarted(hostport string) {
	dialGuardState.Lock()
	defer dialGuardState.Unlock()
	dialGuardState.started[hostport] = true
}

// guardDial is the package's dialGuard under test.
func guardDial(raw string) error {
	u, err := url.Parse(raw)
	host := raw
	if err == nil && u.Host != "" {
		host = u.Host
	}
	dialGuardState.Lock()
	defer dialGuardState.Unlock()
	if dialGuardState.started[host] {
		return nil
	}
	dialGuardState.refused = append(dialGuardState.refused, raw)
	return fmt.Errorf("dial %q: %w (marvel#998)", raw, errNotStarted)
}

// noBroker stands in for the verify dial of a test that does not start a
// broker: it refuses every user, as a host with no broker would, without a
// dial. A manager with no URL would otherwise dial the client's default,
// nats://127.0.0.1:4222.
func noBroker(context.Context, string, string, string) error { return errNoBroker }

var errNoBroker = errors.New("no broker: the test stubs the verify dial")

// listenLoopback listens on a free loopback port and marks it started, for a
// test that stands in for a broker with its own listener.
func listenLoopback(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	markStarted(ln.Addr().String())
	return ln
}

// forgetRefused drops raw from the refused list, for the test that provokes a
// refusal on purpose.
func forgetRefused(raw string) {
	dialGuardState.Lock()
	defer dialGuardState.Unlock()
	dialGuardState.refused = slices.DeleteFunc(dialGuardState.refused, func(s string) bool { return s == raw })
}

// failOnRefusedDials turns a passing run into a failing one when the guard
// refused a dial, and names each refused address.
func failOnRefusedDials(code int) int {
	dialGuardState.Lock()
	defer dialGuardState.Unlock()
	if len(dialGuardState.refused) == 0 {
		return code
	}
	counts := map[string]int{}
	for _, r := range dialGuardState.refused {
		counts[r]++
	}
	fmt.Fprintf(os.Stderr, "FAIL: the dial guard refused %d dial(s) to a broker no test started (marvel#998):\n", len(dialGuardState.refused))
	for _, r := range slices.Sorted(func(yield func(string) bool) {
		for k := range counts {
			if !yield(k) {
				return
			}
		}
	}) {
		fmt.Fprintf(os.Stderr, "\t%q (%d)\n", r, counts[r])
	}
	if code == 0 {
		code = 1
	}
	return code
}

// The guard refuses an address no test started, before any dial, and lets a
// started one through. connectAdmin is tried only on a closed loopback port no
// test marked started (closedLoopbackPort, marvel#1107), so with the guard
// missing the dial is refused by the host and reaches no broker. The live broker's address and an
// empty URL go to guardDial directly, since guardDial never dials.
func TestDialGuardRefusesABrokerNoTestStarted(t *testing.T) {
	closed := "nats://127.0.0.1:" + strconv.Itoa(closedLoopbackPort(t))
	t.Cleanup(func() { forgetRefused(closed) })
	if _, err := connectAdmin(context.Background(), closed, "u", "p"); !errors.Is(err, errNotStarted) {
		t.Fatalf("connectAdmin(%s) = %v, want the guard's refusal", closed, err)
	}
	for _, raw := range []string{"nats://127.0.0.1:4222", ""} {
		t.Cleanup(func() { forgetRefused(raw) })
		if err := guardDial(raw); !errors.Is(err, errNotStarted) {
			t.Errorf("guardDial(%q) = %v, want a refusal", raw, err)
		}
	}
	if err := guardDial("nats://127.0.0.1:" + fmt.Sprint(freePort(t))); err != nil {
		t.Errorf("a port from freePort was refused: %v", err)
	}
}
