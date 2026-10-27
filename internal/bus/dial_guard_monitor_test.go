package bus

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/arcavenae/marvel/internal/config"
)

// The test dial guard covers every dial the package makes (marvel#1007). Three
// of them do not go through connectAdmin: the /varz read in CheckStructure, the
// /leafz read in pollLeaf, and the TCP connect in listenerAnswers. A fourth, the
// /jsz read in ReadHeadroom, was found in review and has its test below. A fixture
// that copied the managed broker's listen address back in would reach the live
// broker's monitor port through them with nothing to say so. Each test hands
// one of the dials an address no test started, a loopback port opened and closed
// here, so a dial that gets past the guard is refused by the host and reaches
// no broker, and checks the guard was asked.

// closedLoopbackPort returns a loopback port nothing listens on and no test
// marked as started.
func closedLoopbackPort(t *testing.T) int {
	t.Helper()
	return closedLoopbackPortFrom(t, func() int {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		_ = ln.Close()
		return port
	})
}

// closedLoopbackPortFrom takes its ports from pick.
func closedLoopbackPortFrom(t *testing.T, pick func() int) int {
	t.Helper()
	return pick()
}

// marvel#1107: the kernel hands out a closed port's number again, so a port
// an earlier test marked started can come back from net.Listen, and the
// guard would let a dial to it through. closedLoopbackPortFrom passes over
// such a port. The picker here returns a marked port first.
func TestClosedLoopbackPortIsNeverOneATestStarted(t *testing.T) {
	marked := freePort(t)
	fresh := closedLoopbackPort(t)
	picks := []int{marked, fresh}
	got := closedLoopbackPortFrom(t, func() int {
		p := picks[0]
		if len(picks) > 1 {
			picks = picks[1:]
		}
		return p
	})
	if got != fresh {
		t.Fatalf("closedLoopbackPortFrom = %d, want %d: %d is marked started", got, fresh, marked)
	}
	addr := "127.0.0.1:" + strconv.Itoa(got)
	t.Cleanup(func() { forgetRefused(addr) })
	if guardDial(addr) == nil {
		t.Errorf("the guard lets %s through, so a dial to it would not be refused", addr)
	}
}

func TestListenerAnswersAsksTheDialGuard(t *testing.T) {
	listen := "127.0.0.1:" + strconv.Itoa(closedLoopbackPort(t))
	t.Cleanup(func() { forgetRefused(listen) })
	s := &Supervisor{mgr: &Manager{bus: config.ResolvedBus{Listen: listen}}}
	if s.listenerAnswers(200 * time.Millisecond) {
		t.Fatalf("a port nothing listens on answered: %s", listen)
	}
	if !guardWasAsked(listen) {
		t.Errorf("listenerAnswers dialed %s without asking the dial guard", listen)
	}
}

func TestPollLeafAsksTheDialGuard(t *testing.T) {
	// MonitorAddr puts the monitor monitorPortOffset above the listen port, so
	// a listen port that far below a closed one makes the monitor a closed port.
	mon := closedLoopbackPort(t)
	if mon-monitorPortOffset < 1 || mon > 65535 {
		t.Skipf("scratch port %d leaves no listen port %d below it", mon, monitorPortOffset)
	}
	listen := "127.0.0.1:" + strconv.Itoa(mon-monitorPortOffset)
	want, err := MonitorAddr(listen)
	if err != nil || want != "127.0.0.1:"+strconv.Itoa(mon) {
		t.Skipf("MonitorAddr(%s) = %q, %v; not the scratch port %d", listen, want, err, mon)
	}
	t.Cleanup(func() { forgetRefused(want) })
	s := &Supervisor{mgr: &Manager{bus: config.ResolvedBus{Listen: listen, HubURL: "nats-leaf://hub.example:9"}}}
	s.pollLeaf()
	if !guardWasAsked(want) {
		t.Errorf("pollLeaf read /leafz at %s without asking the dial guard", want)
	}
}

// CheckStructure reads the broker it is given, which this test starts and the
// guard knows, then reads /varz at a monitor address no test started. The
// broker's own monitor is left off, so the read has nowhere to go either way.
func TestCheckStructureAsksTheDialGuardForTheMonitor(t *testing.T) {
	ns, err := exec.LookPath("nats-server")
	if err != nil {
		t.Skip("nats-server not on PATH")
	}
	port := freePort(t)
	store := filepath.Join(t.TempDir(), "js")
	cmd := exec.Command(ns, "-a", "127.0.0.1", "-p", strconv.Itoa(port), "-js", "-sd", store)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	url := "nats://127.0.0.1:" + strconv.Itoa(port)
	eventually(t, "broker listening", func() bool {
		nc, err := nats.Connect(url, nats.MaxReconnects(0), nats.Timeout(200*time.Millisecond))
		if err != nil {
			return false
		}
		nc.Close()
		return true
	})
	monitor := "127.0.0.1:" + strconv.Itoa(closedLoopbackPort(t))
	t.Cleanup(func() { forgetRefused(monitor) })
	_, err = CheckStructure(context.Background(), url, "anyone", "anything", monitor)
	if !errors.Is(err, errNotStarted) {
		t.Fatalf("CheckStructure read /varz at %s = %v, want the dial guard's refusal", monitor, err)
	}
}

// ReadHeadroom reads /jsz at the monitor address, a fourth read the first three
// tests missed (marvel#1007 review). The address is a closed loopback port no
// test started, so a read that gets past the guard is refused by the host.
func TestReadHeadroomAsksTheDialGuard(t *testing.T) {
	monitor := "127.0.0.1:" + strconv.Itoa(closedLoopbackPort(t))
	t.Cleanup(func() { forgetRefused(monitor) })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := ReadHeadroom(ctx, monitor, t.TempDir()); !errors.Is(err, errNotStarted) {
		t.Fatalf("ReadHeadroom read /jsz at %s = %v, want the dial guard's refusal", monitor, err)
	}
}
