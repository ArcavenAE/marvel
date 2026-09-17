package bus

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/arcavenae/marvel/internal/events"
)

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s: not within 5s", what)
}

func unprovisionedEvents(ring *events.Ring) []events.Event {
	return ring.Snapshot(events.Filter{Kind: events.KindBusUnprovisioned}, 0)
}

// The ticket's verify case: AGENT_INBOX deleted by hand while the broker
// runs. New spawns hold, bus.unprovisioned fires once naming the stream,
// the next tick re-creates it and clears the hold, and no path restarts
// the process.
func TestStructuralMissHoldsReprovisionsAndNeverRestarts(t *testing.T) {
	s, m, ring := newTestSupervisor(t, "")
	// AfterReady is the real provisioner behind a gate the test can close,
	// so the hold is observable between the miss and the repair.
	var mu sync.Mutex
	blocked := false
	s.AfterReady = func() error {
		mu.Lock()
		b := blocked
		mu.Unlock()
		if b {
			return errors.New("provisioning held by the test")
		}
		admin := m.Admin()
		_, err := Provision(context.Background(), m.URL(), admin.Name, admin.Password)
		return err
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	pid := s.Status().PID
	if !s.Ready() {
		t.Fatal("not ready after start")
	}
	st := s.Status()
	if st.Structure == nil || !st.Structure.Provisioned || !st.Structure.Authorized {
		t.Fatalf("first reading = %+v, want provisioned and authorized", st.Structure)
	}
	if st.Structure.TLS {
		t.Fatalf("tls reported true on a plaintext listener")
	}

	// The miss.
	mu.Lock()
	blocked = true
	mu.Unlock()
	admin := m.Admin()
	nc, err := nats.Connect(m.URL(), nats.UserInfo(admin.Name, admin.Password))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)
	if err := js.DeleteStream(context.Background(), InboxStream); err != nil {
		t.Fatal(err)
	}
	eventually(t, "hold after the miss", func() bool { return !s.Ready() })
	st = s.Status()
	if st.Structure == nil || st.Structure.Provisioned || strings.Join(st.Structure.Missing, ",") != InboxStream {
		t.Fatalf("status after the miss = %+v, want AGENT_INBOX missing", st.Structure)
	}
	if !st.Structure.Authorized {
		t.Errorf("authorization read false during an object miss")
	}
	// Several ticks pass while held: one event, not one per tick.
	time.Sleep(3 * s.leafPoll)
	evs := unprovisionedEvents(ring)
	if len(evs) != 1 || !strings.Contains(evs[0].Message, InboxStream) || evs[0].Severity != events.SeverityWarning {
		t.Fatalf("bus.unprovisioned events = %+v, want exactly one naming AGENT_INBOX", evs)
	}

	// The repair: the gate opens, the next tick re-provisions.
	mu.Lock()
	blocked = false
	mu.Unlock()
	eventually(t, "release after re-provision", s.Ready)
	if _, err := js.Stream(context.Background(), InboxStream); err != nil {
		t.Fatalf("AGENT_INBOX not re-created: %v", err)
	}
	st = s.Status()
	if st.Structure == nil || !st.Structure.Provisioned || len(st.Structure.Missing) != 0 {
		t.Errorf("status after repair = %+v", st.Structure)
	}
	if st.PID != pid || st.Restarts != 0 {
		t.Fatalf("broker restarted on a structural miss: pid %d -> %d, restarts %d", pid, st.PID, st.Restarts)
	}
	if n := len(unprovisionedEvents(ring)); n != 1 {
		t.Errorf("bus.unprovisioned events after repair = %d, want still 1", n)
	}
}

// An authorization miss gets one SIGHUP, then holds; a reading that
// returns to authorized releases and re-arms; a read error changes
// nothing. Driven through the injectable reader against a real broker so
// the SIGHUP goes to a real pid.
func TestStructuralAuthMissReloadsOnceThenHolds(t *testing.T) {
	s, m, ring := newTestSupervisor(t, "")
	s.AfterReady = func() error {
		admin := m.Admin()
		_, err := Provision(context.Background(), m.URL(), admin.Name, admin.Password)
		return err
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !s.Ready() {
		t.Fatal("not ready after start")
	}
	var mu sync.Mutex
	reading := Structure{Authorized: true}
	var readErr error
	s.mu.Lock()
	s.checkStructureFn = func(context.Context) (Structure, error) {
		mu.Lock()
		defer mu.Unlock()
		return reading, readErr
	}
	s.mu.Unlock()
	set := func(st Structure, err error) {
		mu.Lock()
		reading, readErr = st, err
		mu.Unlock()
	}

	set(Structure{Authorized: false}, nil)
	s.checkStructure(context.Background())
	if s.Ready() {
		t.Fatal("ready with authorization not loaded")
	}
	s.mu.Lock()
	armed := s.authReloaded
	s.mu.Unlock()
	if !armed {
		t.Fatal("no SIGHUP sent on the first authorization miss")
	}
	if n := len(unprovisionedEvents(ring)); n != 1 {
		t.Fatalf("events after the miss = %d, want 1", n)
	}
	// Still missing on the next check: no second event, still held.
	s.checkStructure(context.Background())
	if s.Ready() || len(unprovisionedEvents(ring)) != 1 {
		t.Fatalf("second check: ready=%v events=%d", s.Ready(), len(unprovisionedEvents(ring)))
	}
	// A read error is no information: nothing changes.
	set(Structure{}, errors.New("monitor unreachable"))
	s.checkStructure(context.Background())
	if s.Ready() || len(unprovisionedEvents(ring)) != 1 {
		t.Fatalf("after a read error: ready=%v events=%d", s.Ready(), len(unprovisionedEvents(ring)))
	}
	// Restored: released, re-armed for the next miss.
	set(Structure{Authorized: true}, nil)
	s.checkStructure(context.Background())
	if !s.Ready() {
		t.Fatal("not released after authorization returned")
	}
	s.mu.Lock()
	armed = s.authReloaded
	s.mu.Unlock()
	if armed {
		t.Error("authReloaded not re-armed after recovery")
	}
	if st := s.Status(); st.Restarts != 0 {
		t.Errorf("restarts = %d on an authorization miss", st.Restarts)
	}
	// A change in the problem set is a new event.
	set(Structure{Authorized: true, Missing: []string{AuditStream}}, nil)
	s.AfterReady = nil // no re-provision, so the miss stands
	s.checkStructure(context.Background())
	if n := len(unprovisionedEvents(ring)); n != 2 {
		t.Errorf("events after a different miss = %d, want 2", n)
	}
}

// CheckStructure against a bare broker started by hand: every object
// missing, authorization off. This is the finding-166 fleet, read
// honestly.
func TestCheckStructureReadsABareBroker(t *testing.T) {
	ns, err := exec.LookPath("nats-server")
	if err != nil {
		t.Skip("nats-server not on PATH")
	}
	port := freePort(t)
	mon := freePort(t)
	store := filepath.Join(t.TempDir(), "js")
	cmd := exec.Command(ns, "-a", "127.0.0.1", "-p", strconv.Itoa(port), "-m", strconv.Itoa(mon), "-js", "-sd", store)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	url := "nats://127.0.0.1:" + strconv.Itoa(port)
	eventually(t, "bare broker listening", func() bool {
		nc, err := nats.Connect(url, nats.MaxReconnects(0), nats.Timeout(200*time.Millisecond))
		if err != nil {
			return false
		}
		nc.Close()
		return true
	})
	st, err := CheckStructure(context.Background(), url, "anyone", "anything", "127.0.0.1:"+strconv.Itoa(mon))
	if err != nil {
		t.Fatal(err)
	}
	if st.Authorized || st.TLS || strings.Join(st.Missing, ",") != "AGENT_AUDIT,AGENT_INBOX,AGENT_STATE" {
		t.Errorf("bare broker reads %+v", st)
	}
	if st.Healthy() || st.Problems() == "" {
		t.Errorf("bare broker reads healthy: %+v", st)
	}
}
