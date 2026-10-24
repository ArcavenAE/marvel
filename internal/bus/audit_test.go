package bus

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// auditHarness is a started, provisioned test broker with an admin JetStream
// handle. provisioning can be held, so a test can delete AGENT_AUDIT without
// the supervisor's structure tick putting it back.
type auditHarness struct {
	m    *Manager
	js   jetstream.JetStream
	hold *atomic.Bool
}

func newAuditHarness(t *testing.T) *auditHarness {
	t.Helper()
	s, m, _ := newTestSupervisor(t, "")
	hold := &atomic.Bool{}
	s.AfterReady = func() error {
		if hold.Load() {
			return errors.New("provisioning held by the test")
		}
		admin := m.Admin()
		_, err := Provision(context.Background(), m.URL(), admin.Name, admin.Password)
		return err
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	admin := m.Admin()
	nc, err := nats.Connect(m.URL(), nats.UserInfo(admin.Name, admin.Password))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	return &auditHarness{m: m, js: js, hold: hold}
}

func (h *auditHarness) publish(t *testing.T, hdr nats.Header, body string) uint64 {
	t.Helper()
	ack, err := h.js.PublishMsg(context.Background(), &nats.Msg{Subject: "agent.audit", Header: hdr, Data: []byte(body)})
	if err != nil {
		t.Fatal(err)
	}
	return ack.Sequence
}

func (h *auditHarness) read(seq uint64) (AuditRecord, error) {
	admin := h.m.Admin()
	return ReadAuditRecord(context.Background(), h.m.URL(), admin.Name, admin.Password, seq)
}

func (h *auditHarness) stream(t *testing.T) jetstream.Stream {
	t.Helper()
	st, err := h.js.Stream(context.Background(), AuditStream)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func wantMiss(t *testing.T, err error, kind AuditMissKind, seq, first, last uint64) {
	t.Helper()
	var miss *AuditMiss
	if !errors.As(err, &miss) {
		t.Fatalf("err = %v, want an *AuditMiss", err)
	}
	if miss.Kind != kind || miss.Seq != seq || miss.First != first || miss.Last != last {
		t.Errorf("miss = %+v, want kind %d seq %d first %d last %d", *miss, kind, seq, first, last)
	}
}

func TestReadAuditRecordReturnsTheRefusalHeadersAndBodyUnchanged(t *testing.T) {
	h := newAuditHarness(t)
	hdr := nats.Header{}
	hdr.Set("Director-Outcome", "refused")
	hdr.Set("Director-Stage", "scope")
	hdr.Set("Director-Refusal", "not permitted")
	hdr.Set("Nats-Msg-Id", "abc-refused")
	hdr.Set("Director-Other", "must not be returned")
	body := `{"to":"a","text":"hello"}`
	seq := h.publish(t, hdr, body)

	rec, err := h.read(seq)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Seq != seq || rec.Subject != "agent.audit" || rec.Time.IsZero() {
		t.Errorf("record = %+v, want seq %d on agent.audit with a time", rec, seq)
	}
	if rec.Outcome != "refused" || rec.Stage != "scope" || rec.Refusal != "not permitted" || rec.MsgID != "abc-refused" {
		t.Errorf("headers = %q %q %q %q", rec.Outcome, rec.Stage, rec.Refusal, rec.MsgID)
	}
	if !bytes.Equal(rec.Body, []byte(body)) {
		t.Errorf("body = %q, want %q", rec.Body, body)
	}
}

func TestReadAuditRecordWithoutHeadersLeavesThemEmpty(t *testing.T) {
	h := newAuditHarness(t)
	seq := h.publish(t, nil, "plain")
	rec, err := h.read(seq)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != "" || rec.Stage != "" || rec.Refusal != "" {
		t.Errorf("record = %+v, want no director headers", rec)
	}
	if string(rec.Body) != "plain" {
		t.Errorf("body = %q", rec.Body)
	}
}

func TestReadAuditRecordNamesEachMiss(t *testing.T) {
	h := newAuditHarness(t)

	// The stream exists and holds nothing.
	_, err := h.read(1)
	wantMiss(t, err, AuditEmpty, 1, 0, 0)

	for i := 0; i < 5; i++ {
		h.publish(t, nil, "r")
	}
	st := h.stream(t)
	// A number past the last record.
	_, err = h.read(6)
	wantMiss(t, err, AuditPastEnd, 6, 1, 5)

	// A number inside the range with no message: deleted, never aged out and
	// never past the end.
	if err := st.DeleteMsg(context.Background(), 3); err != nil {
		t.Fatal(err)
	}
	_, err = h.read(3)
	wantMiss(t, err, AuditGap, 3, 1, 5)

	// Deleting from the front moves the first sequence.
	for _, n := range []uint64{1, 2} {
		if err := st.DeleteMsg(context.Background(), n); err != nil {
			t.Fatal(err)
		}
	}
	_, err = h.read(2)
	wantMiss(t, err, AuditAgedOut, 2, 4, 5)

	// The records that remain still read.
	if _, err := h.read(4); err != nil {
		t.Errorf("seq 4: %v", err)
	}
}

func TestReadAuditRecordWithNoStream(t *testing.T) {
	h := newAuditHarness(t)
	h.hold.Store(true)
	if err := h.js.DeleteStream(context.Background(), AuditStream); err != nil {
		t.Fatal(err)
	}
	_, err := h.read(1)
	wantMiss(t, err, AuditNoStream, 1, 0, 0)
}

func TestReadAuditRecordConnectFailureIsNotAMiss(t *testing.T) {
	h := newAuditHarness(t)
	_, err := ReadAuditRecord(context.Background(), h.m.URL(), h.m.Admin().Name, "wrong-password", 1)
	if err == nil {
		t.Fatal("a wrong password read a record")
	}
	var miss *AuditMiss
	if errors.As(err, &miss) {
		t.Errorf("a connect failure came back as a miss: %v", err)
	}
	if !strings.Contains(err.Error(), "connect") {
		t.Errorf("err = %v, want the connect error", err)
	}
}

func TestReadAuditRecordCreatesNoConsumer(t *testing.T) {
	h := newAuditHarness(t)
	seq := h.publish(t, nil, "r")
	count := func() int {
		info, err := h.stream(t).Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return info.State.Consumers
	}
	before := count()
	if _, err := h.read(seq); err != nil {
		t.Fatal(err)
	}
	if after := count(); after != before {
		t.Errorf("consumers %d before the read, %d after", before, after)
	}
}
