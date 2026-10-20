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

// startScratchBroker runs a plain nats-server on a random localhost port in
// scratch, with JetStream limited to maxFileStore bytes when it is above
// zero, and returns its URL. Never the live bus.
func startScratchBroker(t *testing.T, maxFileStore int) string {
	t.Helper()
	ns, err := exec.LookPath("nats-server")
	if err != nil {
		t.Skip("nats-server not on PATH")
	}
	port := freePort(t)
	dir := t.TempDir()
	conf := filepath.Join(dir, "n.conf")
	body := "listen: 127.0.0.1:" + strconv.Itoa(port) + "\njetstream {\n  store_dir: \"" + filepath.Join(dir, "js") + "\"\n"
	if maxFileStore > 0 {
		body += "  max_file_store: " + strconv.Itoa(maxFileStore) + "\n"
	}
	body += "}\n"
	if err := os.WriteFile(conf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(ns, "-c", conf)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	url := "nats://127.0.0.1:" + strconv.Itoa(port)
	eventually(t, "scratch broker listening", func() bool {
		nc, err := nats.Connect(url, nats.MaxReconnects(0), nats.Timeout(200*time.Millisecond))
		if err != nil {
			return false
		}
		nc.Close()
		return true
	})
	return url
}

func streamMsgs(t *testing.T, js jetstream.JetStream, name string) uint64 {
	t.Helper()
	s, err := js.Stream(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return info.State.Msgs
}

// A healthy broker reads clean, and the probe leaves nothing behind: no
// canary message in either stream and no canary key in the bucket.
func TestProbeWritesOnAHealthyBrokerFindsNothingAndLeavesNoCanary(t *testing.T) {
	url := startScratchBroker(t, 0)
	ctx := context.Background()
	if _, err := Provision(ctx, url, "any", "any"); err != nil {
		t.Fatal(err)
	}
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)
	inboxBefore, auditBefore := streamMsgs(t, js, InboxStream), streamMsgs(t, js, AuditStream)

	for i := 0; i < 3; i++ {
		got, err := ProbeWrites(ctx, url, "any", "any")
		if err != nil {
			t.Fatalf("probe %d: %v", i, err)
		}
		if len(got.Failures) != 0 || got.Problems() != "" {
			t.Fatalf("probe %d on a healthy broker: %+v", i, got)
		}
	}
	if n := streamMsgs(t, js, InboxStream); n != inboxBefore {
		t.Errorf("%s holds %d messages after the probe, want %d: a canary was left", InboxStream, n, inboxBefore)
	}
	if n := streamMsgs(t, js, AuditStream); n != auditBefore {
		t.Errorf("%s holds %d messages after the probe, want %d: a canary was left", AuditStream, n, auditBefore)
	}
	kv, err := js.KeyValue(ctx, StateBucket)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := kv.Keys(ctx)
	if err != nil && !errors.Is(err, jetstream.ErrNoKeysFound) {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Errorf("%s holds keys %v after the probe, want none", StateBucket, keys)
	}
}

// The 2026-09-24 wedge: every read answers, every write is refused. The
// store is filled to its limit, then the probe must name the objects it
// could not write.
func TestProbeWritesNamesAStoreThatRefusesWrites(t *testing.T) {
	url := startScratchBroker(t, 256*1024)
	ctx := context.Background()
	if _, err := Provision(ctx, url, "any", "any"); err != nil {
		t.Fatal(err)
	}
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)
	big := make([]byte, 60*1024)
	refused := false
	for i := 0; i < 20 && !refused; i++ {
		pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, err := js.Publish(pctx, "agent.t.w.x.inbox", big)
		cancel()
		refused = err != nil
	}
	if !refused {
		t.Fatal("could not fill the scratch store to its limit")
	}

	got, err := ProbeWrites(ctx, url, "any", "any")
	if err != nil {
		t.Fatalf("a refused write is a reading, not an error: %v", err)
	}
	if len(got.Failures) == 0 {
		t.Fatal("probe read clean against a store that refuses writes")
	}
	names := got.Problems()
	for _, want := range []string{InboxStream, StateBucket} {
		if !strings.Contains(names, want) {
			t.Errorf("failing set %q does not name %s", names, want)
		}
	}
}

// No broker to ask is no information, not a failed write.
func TestProbeWritesWithNoBrokerIsAnError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got, err := ProbeWrites(ctx, "nats://127.0.0.1:"+strconv.Itoa(freePort(t)), "any", "any")
	if err == nil {
		t.Fatalf("probe against nothing returned a reading: %+v", got)
	}
}

func writeFailedEvents(ring *events.Ring) []events.Event {
	return ring.Snapshot(events.Filter{Kind: events.KindBusStoreWriteFailed}, 0)
}

// The supervisor acts on a failing probe by telling the operator once per
// change, never by restarting the broker, and never by holding spawns.
func TestWriteFailureEscalatesOncePerChangeAndNeverRestartsOrHolds(t *testing.T) {
	s, _, ring := newTestSupervisor(t, "")
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !s.Ready() {
		t.Fatal("not ready after start")
	}
	pid := s.Status().PID
	var mu sync.Mutex
	reading := WriteReading{}
	var readErr error
	s.mu.Lock()
	s.writeProbeFn = func(context.Context) (WriteReading, error) {
		mu.Lock()
		defer mu.Unlock()
		return reading, readErr
	}
	s.mu.Unlock()
	set := func(w WriteReading, err error) {
		mu.Lock()
		reading, readErr = w, err
		mu.Unlock()
	}

	set(WriteReading{Failures: []WriteFailure{{Object: StateBucket, Err: "no space left on device"}}}, nil)
	s.checkStructure(context.Background())
	s.checkStructure(context.Background())
	evs := writeFailedEvents(ring)
	if len(evs) != 1 || evs[0].Severity != events.SeverityError || !strings.Contains(evs[0].Message, StateBucket) {
		t.Fatalf("bus.store.write-failed events = %+v, want exactly one error naming %s", evs, StateBucket)
	}
	if !s.Ready() {
		t.Error("a write failure held new spawns; it must only report")
	}
	if st := s.Status(); st.WriteFailed != StateBucket || st.PID != pid || st.Restarts != 0 {
		t.Fatalf("status = %+v, want write_failed %s and no restart", st, StateBucket)
	}

	// A read error is no information: nothing changes.
	set(WriteReading{}, errors.New("broker unreachable"))
	s.checkStructure(context.Background())
	if len(writeFailedEvents(ring)) != 1 || s.Status().WriteFailed == "" {
		t.Fatal("a probe error changed the reading")
	}

	// A different failing set is a new event.
	set(WriteReading{Failures: []WriteFailure{{Object: StateBucket, Err: "x"}, {Object: InboxStream, Err: "y"}}}, nil)
	s.checkStructure(context.Background())
	if n := len(writeFailedEvents(ring)); n != 2 {
		t.Fatalf("events after a wider failing set = %d, want 2", n)
	}

	// Recovery clears the status and is not an error event.
	set(WriteReading{}, nil)
	s.checkStructure(context.Background())
	if n := len(writeFailedEvents(ring)); n != 2 {
		t.Errorf("events after recovery = %d, want still 2", n)
	}
	if st := s.Status(); st.WriteFailed != "" || st.Restarts != 0 || st.PID != pid {
		t.Errorf("status after recovery = %+v", st)
	}
}
