package bus

import (
	"context"
	"errors"
	"log"
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

// A declared object that is absent is the structural check's miss, not a
// failed write: the probe skips it and still probes the rest.
func TestProbeWritesSkipsAnAbsentObject(t *testing.T) {
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
	if err := js.DeleteStream(ctx, InboxStream); err != nil {
		t.Fatal(err)
	}
	if err := js.DeleteKeyValue(ctx, StateBucket); err != nil {
		t.Fatal(err)
	}
	got, err := ProbeWrites(ctx, url, "any", "any")
	if err != nil || len(got.Failures) != 0 {
		t.Fatalf("probe with %s and %s absent = %+v, %v; want a clean reading", InboxStream, StateBucket, got, err)
	}
}

// A live stream at a message cap with discard-old would lose a real message
// to every canary. The probe must read the live limits first, leave the
// stream alone, and say it did not test it.
func TestProbeWritesDoesNotEvictFromAStreamAtItsMessageCap(t *testing.T) {
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
	st, err := js.Stream(ctx, InboxStream)
	if err != nil {
		t.Fatal(err)
	}
	cfg := st.CachedInfo().Config
	cfg.MaxMsgs, cfg.Discard = 2, jetstream.DiscardOld
	if _, err := js.UpdateStream(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"real one", "real two"} {
		if _, err := js.Publish(ctx, "agent.t.w.x.inbox", []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	before := mustStreamState(t, st)

	for i := 0; i < 3; i++ {
		got, err := ProbeWrites(ctx, url, "any", "any")
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Failures) != 0 {
			t.Fatalf("a stream at its cap read as a failed write: %+v", got.Failures)
		}
		if len(got.Skipped) != 1 || got.Skipped[0].Object != InboxStream || got.Skipped[0].Reason == "" {
			t.Fatalf("skipped = %+v, want %s skipped with a reason", got.Skipped, InboxStream)
		}
	}
	after := mustStreamState(t, st)
	if after.Msgs != before.Msgs || after.FirstSeq != before.FirstSeq || after.LastSeq != before.LastSeq {
		t.Fatalf("the probe changed a capped stream: before %+v, after %+v", before, after)
	}
}

// The same for a byte cap, and for the bucket's backing stream.
func TestProbeWritesSkipsAStreamAndABucketAtTheirCaps(t *testing.T) {
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

	audit, err := js.Stream(ctx, AuditStream)
	if err != nil {
		t.Fatal(err)
	}
	acfg := audit.CachedInfo().Config
	acfg.MaxBytes, acfg.Discard = 1024, jetstream.DiscardOld
	if _, err := js.UpdateStream(ctx, acfg); err != nil {
		t.Fatal(err)
	}
	if _, err := js.Publish(ctx, "agent.audit", make([]byte, 900)); err != nil {
		t.Fatal(err)
	}

	kv, err := js.KeyValue(ctx, StateBucket)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Put(ctx, "real.key", []byte("keep me")); err != nil {
		t.Fatal(err)
	}
	backing, err := js.Stream(ctx, "KV_"+StateBucket)
	if err != nil {
		t.Fatal(err)
	}
	kcfg := backing.CachedInfo().Config
	kcfg.MaxMsgs, kcfg.Discard = 1, jetstream.DiscardOld
	if _, err := js.UpdateStream(ctx, kcfg); err != nil {
		t.Fatal(err)
	}
	auditBefore, kvBefore := mustStreamState(t, audit), mustStreamState(t, backing)

	got, err := ProbeWrites(ctx, url, "any", "any")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Failures) != 0 {
		t.Fatalf("capped objects read as failed writes: %+v", got.Failures)
	}
	skipped := map[string]bool{}
	for _, sk := range got.Skipped {
		skipped[sk.Object] = true
	}
	if !skipped[AuditStream] || !skipped[StateBucket] {
		t.Fatalf("skipped = %+v, want both %s and %s", got.Skipped, AuditStream, StateBucket)
	}
	if after := mustStreamState(t, audit); after.Msgs != auditBefore.Msgs || after.FirstSeq != auditBefore.FirstSeq || after.LastSeq != auditBefore.LastSeq {
		t.Errorf("the probe changed the capped audit stream: %+v -> %+v", auditBefore, after)
	}
	if after := mustStreamState(t, backing); after.Msgs != kvBefore.Msgs || after.LastSeq != kvBefore.LastSeq {
		t.Errorf("the probe changed the capped bucket: %+v -> %+v", kvBefore, after)
	}
	if e, err := kv.Get(ctx, "real.key"); err != nil || string(e.Value()) != "keep me" {
		t.Errorf("the real key did not survive the probe: %v", err)
	}
}

func mustStreamState(t *testing.T, st jetstream.Stream) jetstream.StreamState {
	t.Helper()
	info, err := st.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return info.State
}

// A skip shows in the status and is told once, as a log line; it is not the
// write-failed event.
func TestSkippedObjectsShowInStatusAndAreNotAWriteFailure(t *testing.T) {
	s, _, ring := newTestSupervisor(t, "")
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.writeProbeFn = func(context.Context) (WriteReading, error) {
		return WriteReading{Skipped: []WriteSkip{{Object: InboxStream, Reason: "at its message cap"}}}, nil
	}
	s.mu.Unlock()
	s.checkStructure(context.Background())
	st := s.Status()
	if st.WriteSkipped != InboxStream {
		t.Fatalf("status write_skipped = %q, want %s", st.WriteSkipped, InboxStream)
	}
	if st.WriteFailed != "" || len(writeFailedEvents(ring)) != 0 || !s.Ready() {
		t.Fatalf("a skip was treated as a failure: %+v", st)
	}
}

// One message of headroom: a stream one message below its cap is skipped
// too, because between the limit read and the write another publisher can
// take the last slot and the canary would evict. A stream with room is still
// probed and left as it was.
func TestProbeWritesKeepsOneMessageOfHeadroomBelowTheCap(t *testing.T) {
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
	st, err := js.Stream(ctx, InboxStream)
	if err != nil {
		t.Fatal(err)
	}
	cfg := st.CachedInfo().Config
	cfg.Discard = jetstream.DiscardOld
	for _, body := range []string{"real one", "real two"} {
		if _, err := js.Publish(ctx, "agent.t.w.x.inbox", []byte(body)); err != nil {
			t.Fatal(err)
		}
	}

	cfg.MaxMsgs = 3 // room for one more, no headroom
	if _, err := js.UpdateStream(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := ProbeWrites(ctx, url, "any", "any")
	if err != nil || len(got.Failures) != 0 {
		t.Fatalf("probe at cap-1: %+v, %v", got, err)
	}
	if len(got.Skipped) != 1 || got.Skipped[0].Object != InboxStream {
		t.Fatalf("a stream one message below its cap was probed: skipped = %+v", got.Skipped)
	}

	cfg.MaxMsgs = 5 // room and headroom
	if _, err := js.UpdateStream(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	got, err = ProbeWrites(ctx, url, "any", "any")
	if err != nil || len(got.Failures) != 0 || len(got.Skipped) != 0 {
		t.Fatalf("probe with headroom: %+v, %v", got, err)
	}
	if n := mustStreamState(t, st).Msgs; n != 2 {
		t.Fatalf("the stream holds %d messages after a probe, want the 2 real ones", n)
	}
}

// Going from failing to skipped is not recovery. The log must say the
// object is no longer failing but is not being tested, and must not say the
// writes succeed again.
func TestFailingToSkippedLogsASkipNotARecovery(t *testing.T) {
	s, _, _ := newTestSupervisor(t, "")
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	reading := WriteReading{Failures: []WriteFailure{{Object: InboxStream, Err: "no space left on device"}}}
	s.mu.Lock()
	s.writeProbeFn = func(context.Context) (WriteReading, error) {
		mu.Lock()
		defer mu.Unlock()
		return reading, nil
	}
	s.mu.Unlock()

	var buf lockedBuffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	s.checkStructure(context.Background())
	mu.Lock()
	reading = WriteReading{Skipped: []WriteSkip{{Object: InboxStream, Reason: "at its message cap"}}}
	mu.Unlock()
	s.checkStructure(context.Background())
	out := buf.String()
	if strings.Contains(out, "succeed again") {
		t.Fatalf("a skipped object was logged as recovered:\n%s", out)
	}
	if !strings.Contains(out, "no longer failing") || !strings.Contains(out, InboxStream+" is skipped") {
		t.Fatalf("the log does not say the object is skipped:\n%s", out)
	}

	// A true recovery still says so.
	mu.Lock()
	reading = WriteReading{}
	mu.Unlock()
	s.checkStructure(context.Background())
	if !strings.Contains(buf.String(), "succeed again") {
		t.Fatalf("a clean reading after skipping did not log recovery:\n%s", buf.String())
	}
}

// lockedBuffer is a log sink the supervisor's tick goroutine and the test
// can both touch.
type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
