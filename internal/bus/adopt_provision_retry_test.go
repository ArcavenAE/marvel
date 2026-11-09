package bus

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/events"
)

// The adopt path signals the broker to reload and then provisions at once. A
// reload in flight closes a connection that is mid-provision, so the first
// attempt can fail with "connection closed" while the credentials are right.
// Measured on a leafed broker: 9 of 300 SIGHUP-then-provision pairs failed
// that way. A successor that gives up there fails its whole start.
func TestAnAdoptedBrokerProvisioningRidesOutTheReloadItJustSent(t *testing.T) {
	s1, m, _ := newTestSupervisor(t, "")
	if err := s1.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	pid := s1.Status().PID
	s1.Stop(true)
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })

	s2, err := NewSupervisor(m, filepath.Dir(s1.pidFile), filepath.Dir(s1.logPath), events.NewRing(16))
	if err != nil {
		t.Fatal(err)
	}
	s2.dialTimeout = loadedBrokerWait
	s2.adoptProvisionWait = time.Millisecond
	attach(t, s2, tableOf(s1))
	var calls atomic.Int32
	s2.AfterReady = func() error {
		if calls.Add(1) <= 2 {
			return errors.New("stream AGENT_INBOX: nats: connection closed")
		}
		return nil
	}
	if err := s2.Start(context.Background()); err != nil {
		t.Fatalf("an adopt whose first two provisions were cut by the reload failed its start: %v", err)
	}
	if got := calls.Load(); got < 3 {
		t.Errorf("provision ran %d times, want at least 3 (two cut, one through)", got)
	}
	if st := s2.Status(); !st.Adopted || st.PID != pid {
		t.Errorf("status = %+v, want adopted pid %d", st, pid)
	}
	s2.Stop(false)
}

// A broker that stays unprovisionable after the retries still fails the
// start with the last error, so the retry hides nothing that persists.
func TestAnAdoptedBrokerThatStaysUnprovisionableStillFailsTheStart(t *testing.T) {
	s1, m, _ := newTestSupervisor(t, "")
	if err := s1.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	pid := s1.Status().PID
	s1.Stop(true)
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })

	s2, err := NewSupervisor(m, filepath.Dir(s1.pidFile), filepath.Dir(s1.logPath), events.NewRing(16))
	if err != nil {
		t.Fatal(err)
	}
	s2.dialTimeout = loadedBrokerWait
	s2.adoptProvisionWait = time.Millisecond
	attach(t, s2, tableOf(s1))
	var calls atomic.Int32
	s2.AfterReady = func() error {
		calls.Add(1)
		return errors.New("stream AGENT_INBOX: nats: authorization violation")
	}
	err = s2.Start(context.Background())
	if err == nil {
		t.Fatal("an adopt whose provisioning never succeeds reported a good start")
	}
	if got := int(calls.Load()); got != adoptProvisionTries {
		t.Errorf("provision ran %d times, want %d", got, adoptProvisionTries)
	}
	s2.Stop(false)
}
