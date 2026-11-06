package jetstream

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	njs "github.com/nats-io/nats.go/jetstream"
)

const (
	testStream = "AGENT_INBOX"
	subjA      = "agent.aae.squad.alpha.inbox"
	subjB      = "agent.aae.squad.role.worker.inbox"
	subjOther  = "agent.aae.squad.beta.inbox"
)

// scratchBroker runs a loopback nats-server with JetStream and a stream holding
// the local inbox subjects, never a fleet broker. It skips when the binary is
// not on PATH.
type scratchBroker struct {
	t   *testing.T
	url string
	nc  *nats.Conn
	js  njs.JetStream
	st  njs.Stream
}

func newScratchBroker(t *testing.T) *scratchBroker { return newScratchBrokerWith(t, "", nil) }

// newScratchBrokerWith is newScratchBroker with an extra fragment of server
// configuration (an authorization block) and the options the setup connection
// connects with.
func newScratchBrokerWith(t *testing.T, extraConf string, opts []nats.Option) *scratchBroker {
	t.Helper()
	bin, err := exec.LookPath("nats-server")
	if err != nil {
		// CI installs it (.github/workflows/ci.yml), so a missing binary there is
		// a failure, never a quiet skip.
		if os.Getenv("CI") != "" {
			t.Fatal("nats-server not on PATH under CI: the broker tests would not run")
		}
		t.Skip("nats-server not on PATH")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	// The server carries JetStream domain "global", as the hub does, so director's
	// global read (JetStream in domain "global") can be run against it. The
	// adapter's own handle is domain-less and the server answers both.
	conf := filepath.Join(t.TempDir(), "nats.conf")
	body := fmt.Sprintf("host: 127.0.0.1\nport: %d\njetstream {\n  store_dir: %q\n  domain: global\n}\n%s", port, t.TempDir(), extraConf)
	if err := os.WriteFile(conf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-c", conf)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start nats-server: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	url := fmt.Sprintf("nats://127.0.0.1:%d", port)
	var nc *nats.Conn
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if nc, err = nats.Connect(url, opts...); err == nil {
			break
		}
	}
	if nc == nil {
		t.Fatalf("nats-server did not come up: %v", err)
	}
	t.Cleanup(nc.Close)
	js, err := njs.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	st, err := js.CreateStream(context.Background(), njs.StreamConfig{Name: testStream, Subjects: []string{"agent.>"}})
	if err != nil {
		t.Fatal(err)
	}
	// The presence buckets the two tiers read, as director's shim creates them.
	for _, bucket := range []string{"AGENT_STATE", "GLOBAL_PRESENCE"} {
		if _, err := js.CreateKeyValue(context.Background(), njs.KeyValueConfig{Bucket: bucket}); err != nil {
			t.Fatal(err)
		}
	}
	return &scratchBroker{t: t, url: url, nc: nc, js: js, st: st}
}

// publish stores one message on subject.
func (b *scratchBroker) publish(subject string) {
	b.t.Helper()
	if _, err := b.js.Publish(context.Background(), subject, []byte("m")); err != nil {
		b.t.Fatal(err)
	}
}

// consumer creates a durable named name reading filters, from the first message.
func (b *scratchBroker) consumer(name string, filters ...string) njs.Consumer {
	b.t.Helper()
	cfg := njs.ConsumerConfig{
		Durable: name, AckPolicy: njs.AckExplicitPolicy, FilterSubjects: filters,
	}
	if len(filters) == 1 {
		cfg.FilterSubject, cfg.FilterSubjects = filters[0], nil
	}
	c, err := b.js.CreateOrUpdateConsumer(context.Background(), testStream, cfg)
	if err != nil {
		b.t.Fatal(err)
	}
	return c
}

// take fetches n messages from c and acks the ones in ackIdx (0-based).
func (b *scratchBroker) take(c njs.Consumer, n int, ackIdx ...int) {
	b.t.Helper()
	batch, err := c.Fetch(n, njs.FetchMaxWait(2*time.Second))
	if err != nil {
		b.t.Fatal(err)
	}
	var msgs []njs.Msg
	for m := range batch.Messages() {
		msgs = append(msgs, m)
	}
	if len(msgs) != n {
		b.t.Fatalf("fetched %d messages, want %d", len(msgs), n)
	}
	for _, i := range ackIdx {
		if err := msgs[i].DoubleAck(context.Background()); err != nil {
			b.t.Fatal(err)
		}
	}
}

// present writes a presence key into bucket, as a live shim does.
func (b *scratchBroker) present(bucket, key, value string) {
	b.t.Helper()
	kv, err := b.js.KeyValue(context.Background(), bucket)
	if err != nil {
		b.t.Fatal(err)
	}
	if _, err := kv.Put(context.Background(), key, []byte(value)); err != nil {
		b.t.Fatal(err)
	}
}

// gone removes a presence key the way a shim that exits cleanly does.
func (b *scratchBroker) gone(bucket, key string) {
	b.t.Helper()
	kv, err := b.js.KeyValue(context.Background(), bucket)
	if err != nil {
		b.t.Fatal(err)
	}
	if err := kv.Delete(context.Background(), key); err != nil {
		b.t.Fatal(err)
	}
}

// consumerCount is how many consumers stream holds.
func (b *scratchBroker) consumerCount(stream string) int {
	b.t.Helper()
	st, err := b.js.Stream(context.Background(), stream)
	if err != nil {
		b.t.Fatal(err)
	}
	info, err := st.Info(context.Background())
	if err != nil {
		b.t.Fatal(err)
	}
	return info.State.Consumers
}

// takeNak fetches n messages from c, refuses the one at nakIdx and acks the one
// at ackIdx, as a seat does when it cannot handle a message yet.
func (b *scratchBroker) takeNak(c njs.Consumer, n, nakIdx, ackIdx int) {
	b.t.Helper()
	batch, err := c.Fetch(n, njs.FetchMaxWait(2*time.Second))
	if err != nil {
		b.t.Fatal(err)
	}
	var msgs []njs.Msg
	for m := range batch.Messages() {
		msgs = append(msgs, m)
	}
	if len(msgs) != n {
		b.t.Fatalf("fetched %d messages, want %d", len(msgs), n)
	}
	if err := msgs[nakIdx].Nak(); err != nil {
		b.t.Fatal(err)
	}
	if err := msgs[ackIdx].DoubleAck(context.Background()); err != nil {
		b.t.Fatal(err)
	}
}
