package bus

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A nats-server that exits at startup, here because its monitoring port is
// taken, is reported at once and by what it said, not after the whole
// listener wait as a server that "did not answer" (#962). Under a loaded
// -race run the +4000 monitoring port of a free client port can be any
// other socket's ephemeral port, and the server exits in milliseconds.
func TestStartReportsAServerThatExitsBeforeItListens(t *testing.T) {
	s, m, _ := newTestSupervisor(t, "")
	mon, err := MonitorAddr(m.bus.Listen)
	if err != nil {
		t.Fatal(err)
	}
	hold, err := net.Listen("tcp", mon)
	if err != nil {
		t.Fatalf("hold the monitoring port %s: %v", mon, err)
	}
	t.Cleanup(func() { _ = hold.Close() })

	began := time.Now()
	err = s.Start(context.Background())
	took := time.Since(began)
	if err == nil {
		t.Fatal("Start succeeded with the monitoring port taken")
	}
	if took > 5*time.Second {
		t.Errorf("Start took %v to fail, want well under the %v listener wait", took, s.dialTimeout)
	}
	if !strings.Contains(err.Error(), "exited") || !strings.Contains(err.Error(), "monitor") {
		t.Errorf("err = %v, want it to say the server exited and quote its log line about the monitor port", err)
	}
}

// A child that execs into another program is still running, so it is not a
// server that exited: the wait runs its whole bound and says the listener did
// not answer (review of #1099). The exec lands well inside the wait here, so
// a check that compared identities would misreport it.
func TestAChildThatExecsIsNotReportedAsExited(t *testing.T) {
	s, _, _ := newTestSupervisor(t, "")
	script := filepath.Join(t.TempDir(), "exec-broker")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	s.binary = script
	s.dialTimeout = 1500 * time.Millisecond
	var spawned atomic.Int64
	register := s.onSpawn
	s.onSpawn = func(pid int) {
		spawned.Store(int64(pid))
		register(pid)
	}
	t.Cleanup(func() { endSpawnedChild(t, int(spawned.Load())) })
	err := s.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "did not answer on") {
		t.Fatalf("Start = %v, want the listener timeout for a child that is still running", err)
	}
}
