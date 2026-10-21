package bus

import (
	"bufio"
	"context"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/events"
)

// parityGolden is the bus.* event list of the parity sequence, recorded
// against bus.Supervisor before its lifecycle moved into workload.Process
// (restructure plan, Move 2, PR 2b). PR 2c asserts the same list from the
// driver. Readiness is not part of parity: it becomes stricter by design.
const parityGolden = "testdata/parity-events.golden"

// readGolden returns the kinds in a golden file: one per line, with blank
// lines and # comments skipped.
func readGolden(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatalf("%s holds no kinds", path)
	}
	return out
}

// busKinds is every bus.* kind the ring holds, in order.
func busKinds(r *events.Ring) []string {
	var out []string
	for _, e := range r.Snapshot(events.Filter{}, 0) {
		if strings.HasPrefix(string(e.Kind), "bus.") {
			out = append(out, string(e.Kind))
		}
	}
	return out
}

// The parity sequence (restructure plan, Move 2, "event parity"): a stale
// pidfile, an unproven port, a fresh start, a structural miss, a hub with no
// leaf credential, a crash, a restart and a stop, on one supervisor. The
// ticker is an hour, so no tick lands inside the sequence, and the structural
// reader reports the same miss every time, so its event fires once.
func TestParitySequenceEmitsTheGoldenBusEvents(t *testing.T) {
	s, _, ring := newTestSupervisor(t, "nats-leaf://127.0.0.1:1")
	s.AfterReady = nil
	s.leafPoll = time.Hour
	s.checkStructureFn = func(context.Context) (Structure, error) {
		return Structure{Missing: []string{"stream AGENT_INBOX"}, Authorized: true}, nil
	}
	s.writeProbeFn = func(context.Context) (WriteReading, error) { return WriteReading{}, nil }
	pt := tableOf(s)

	// A stale pidfile, and the port held by a process it does not prove.
	pid := reapedPid(t)
	pt.plant(pid, brokerIdentity(s, "darwin:200.000001"))
	writePidfile(t, s, pid)
	writeSidecar(t, s, pid, brokerIdentity(s, "darwin:100.000000"))
	l, err := net.Listen("tcp", s.mgr.bus.Listen)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(context.Background()); err == nil {
		t.Fatal("Start adopted a port the pidfile does not prove")
	}
	_ = l.Close()

	// A fresh start, with the structural miss and the hub that has no leaf
	// credential.
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("fresh Start: %v", err)
	}
	waitUntil(t, "the first bus.started", func() bool { return startedCount(ring) == 1 })

	// A crash, and the respawn after the backoff.
	first := s.Status().PID
	if first <= 1 { // kill(0) would end this test's own process group
		t.Fatalf("no broker pid to crash (%d)", first)
	}
	if err := syscall.Kill(first, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the respawn", func() bool { return startedCount(ring) == 2 && s.Status().PID != first })

	// A requested restart, then a stop.
	if err := s.Restart("parity"); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	waitUntil(t, "the restart's bus.started", func() bool { return startedCount(ring) == 3 })
	s.Stop(false)

	want := readGolden(t, parityGolden)
	got := busKinds(ring)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("bus.* kinds:\n%s\nwant (%s):\n%s", strings.Join(got, "\n"), parityGolden, strings.Join(want, "\n"))
	}
}
