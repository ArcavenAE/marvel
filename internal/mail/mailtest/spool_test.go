package mailtest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/mail"
)

var (
	t0   = time.Date(2026, 11, 6, 3, 0, 0, 0, time.UTC)
	seat = api.Session{Workspace: "aae", Name: "builder-0"}
)

// newSpool is a spool whose clock is fixed at t0 plus the given offset.
func newSpool(t *testing.T, now time.Time) *Spool {
	t.Helper()
	return New(t, func() time.Time { return now })
}

// need returns a reading's value, failing the test if it holds none.
func need[T any](t *testing.T, name string, c interface{ Value() (T, bool) }) T {
	t.Helper()
	v, ok := c.Value()
	if !ok {
		t.Fatalf("%s has no value", name)
	}
	return v
}

// The spool is a MailReader, so a queue that is not NATS fits the contract.
var _ mail.MailReader = (*Spool)(nil)

func TestTheSpoolReportsDepthOldestAgeAndLastAck(t *testing.T) {
	s := newSpool(t, t0.Add(10*time.Minute))
	addr := s.AddressOf(seat)
	// Messages 1 and 2 are acked (floor 2); 3, 4 and 5 are unread, the oldest
	// stored 7 minutes before the spool's clock.
	s.Deliver(addr, 1, t0)
	s.Deliver(addr, 2, t0.Add(1*time.Minute))
	s.Deliver(addr, 3, t0.Add(3*time.Minute))
	s.Deliver(addr, 4, t0.Add(5*time.Minute))
	s.Deliver(addr, 5, t0.Add(9*time.Minute))
	s.Ack(addr, 2, t0.Add(2*time.Minute))

	got, err := s.Read(context.Background(), seat)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if v := need[int64](t, "Depth", got.Depth); v != 3 {
		t.Errorf("Depth = %d, want 3", v)
	}
	if v := need[time.Duration](t, "OldestAge", got.OldestAge); v != 7*time.Minute {
		t.Errorf("OldestAge = %v, want 7m", v)
	}
	if v := need[time.Time](t, "LastAckAt", got.LastAckAt); !v.Equal(t0.Add(2 * time.Minute)) {
		t.Errorf("LastAckAt = %v, want %v", v, t0.Add(2*time.Minute))
	}
	if v := need[uint64](t, "AckFloor", got.AckFloor); v != 2 {
		t.Errorf("AckFloor = %d, want 2", v)
	}
	if v := need[uint64](t, "OldestSeq", got.OldestSeq); v != 3 {
		t.Errorf("OldestSeq = %d, want 3", v)
	}
	if v := need[int64](t, "AckPending", got.AckPending); v != 0 {
		t.Errorf("AckPending = %d, want 0: a spool has no handled mode", v)
	}
	if got.Source != "spool" || !got.ObservedAt.Equal(t0.Add(10*time.Minute)) {
		t.Errorf("Source = %q, ObservedAt = %v", got.Source, got.ObservedAt)
	}
}

// A seat that was never acked reads "never" for the last ack, and its whole
// inbox is unread from floor 0.
func TestASeatThatNeverAckedReadsNever(t *testing.T) {
	s := newSpool(t, t0.Add(time.Hour))
	addr := s.AddressOf(seat)
	s.Deliver(addr, 1, t0)
	s.Deliver(addr, 2, t0.Add(time.Minute))

	got, err := s.Read(context.Background(), seat)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if k := got.LastAckAt.Kind(); k != mail.KindNever {
		t.Errorf("LastAckAt reads %v, want never", k)
	}
	if v := need[int64](t, "Depth", got.Depth); v != 2 {
		t.Errorf("Depth = %d, want 2", v)
	}
	if v := need[uint64](t, "AckFloor", got.AckFloor); v != 0 {
		t.Errorf("AckFloor = %d, want 0", v)
	}
	if v := need[time.Duration](t, "OldestAge", got.OldestAge); v != time.Hour {
		t.Errorf("OldestAge = %v, want 1h", v)
	}
}

// Nothing unread is a real zero, not an absence: depth 0, age 0, no oldest.
func TestNothingUnreadIsARealZero(t *testing.T) {
	s := newSpool(t, t0.Add(time.Hour))
	addr := s.AddressOf(seat)
	s.Deliver(addr, 1, t0)
	s.Ack(addr, 1, t0.Add(time.Minute))

	got, err := s.Read(context.Background(), seat)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if v := need[int64](t, "Depth", got.Depth); v != 0 {
		t.Errorf("Depth = %d, want 0", v)
	}
	if v := need[time.Duration](t, "OldestAge", got.OldestAge); v != 0 {
		t.Errorf("OldestAge = %v, want 0", v)
	}
	if v := need[uint64](t, "OldestSeq", got.OldestSeq); v != 0 {
		t.Errorf("OldestSeq = %d, want 0 (none)", v)
	}
}

// A seat the spool has no directory for has no inbox to read: that is unknown,
// with an error, and never a depth of zero.
func TestASeatWithNoSpoolDirectoryIsAnError(t *testing.T) {
	s := newSpool(t, t0)
	got, err := s.Read(context.Background(), seat)
	if err == nil {
		t.Fatal("Read of a seat with no inbox returned no error")
	}
	if got.Depth.Kind() != mail.KindUnknown {
		t.Errorf("Depth reads %v, want unknown", got.Depth.Kind())
	}
}

// An injected error is the reader's error, and the state beside it reads
// unknown in every field. Only the call asked for fails.
func TestAnInjectedErrorReturnsAnErrorAndAnUnknownState(t *testing.T) {
	s := newSpool(t, t0.Add(time.Minute))
	addr := s.AddressOf(seat)
	s.Deliver(addr, 1, t0)
	boom := errors.New("spool: disk on fire")
	s.FailOn(2, boom)

	if _, err := s.Read(context.Background(), seat); err != nil {
		t.Fatalf("call 1: %v", err)
	}
	got, err := s.Read(context.Background(), seat)
	if !errors.Is(err, boom) {
		t.Fatalf("call 2 error = %v, want the injected one", err)
	}
	for name, k := range map[string]mail.Kind{
		"Depth": got.Depth.Kind(), "OldestAge": got.OldestAge.Kind(), "LastAckAt": got.LastAckAt.Kind(),
		"AckPending": got.AckPending.Kind(), "AckFloor": got.AckFloor.Kind(), "OldestSeq": got.OldestSeq.Kind(),
	} {
		if k != mail.KindUnknown {
			t.Errorf("after an error %s reads %v, want unknown", name, k)
		}
	}
	if got.Note == "" {
		t.Error("the state beside an error carries no note of the cause")
	}
	if _, err := s.Read(context.Background(), seat); err != nil {
		t.Errorf("call 3 failed too: %v", err)
	}
}

// A cancelled context is an error and an unknown state, not a reading.
func TestACancelledContextIsAnError(t *testing.T) {
	s := newSpool(t, t0)
	s.Deliver(s.AddressOf(seat), 1, t0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := s.Read(ctx, seat)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if got.Depth.Kind() != mail.KindUnknown {
		t.Errorf("Depth reads %v, want unknown", got.Depth.Kind())
	}
}

// One field at a time can be forced to unknown or unsupported, and the others
// still read their values.
func TestAFieldCanBeForcedUnknownOrUnsupported(t *testing.T) {
	s := newSpool(t, t0.Add(time.Minute))
	addr := s.AddressOf(seat)
	s.Deliver(addr, 1, t0)
	s.SetField(FieldOldestAge, mail.KindUnsupported)
	s.SetField(FieldLastAckAt, mail.KindUnknown)

	got, err := s.Read(context.Background(), seat)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if k := got.OldestAge.Kind(); k != mail.KindUnsupported {
		t.Errorf("OldestAge reads %v, want unsupported", k)
	}
	if k := got.LastAckAt.Kind(); k != mail.KindUnknown {
		t.Errorf("LastAckAt reads %v, want unknown", k)
	}
	if v := need[int64](t, "Depth", got.Depth); v != 1 {
		t.Errorf("Depth = %d, want 1: a forced field leaves the others alone", v)
	}
}

// Two seats read two spool directories.
func TestSeatsDoNotShareAnInbox(t *testing.T) {
	s := newSpool(t, t0.Add(time.Minute))
	other := api.Session{Workspace: "aae", Name: "builder-1"}
	s.Deliver(s.AddressOf(seat), 1, t0)
	s.Deliver(s.AddressOf(other), 1, t0)
	s.Deliver(s.AddressOf(other), 2, t0)

	a, err := s.Read(context.Background(), seat)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Read(context.Background(), other)
	if err != nil {
		t.Fatal(err)
	}
	if va, vb := need[int64](t, "a", a.Depth), need[int64](t, "b", b.Depth); va != 1 || vb != 2 {
		t.Errorf("depths = %d, %d; want 1, 2", va, vb)
	}
}
