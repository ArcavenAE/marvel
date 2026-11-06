package jetstream

import (
	"context"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/mail"
	njs "github.com/nats-io/nats.go/jetstream"
)

var _ mail.Queue = (*Queue)(nil)

// newQueue is the local-tier reader the broker tests build, over the AGENT_STATE
// bucket the scratch broker creates.
func newQueue(b *scratchBroker) *Queue { return NewQueue(b.js, LocalPresence) }

func durableByName(t *testing.T, d mail.Durables, name string) mail.Durable {
	t.Helper()
	for _, x := range d.List {
		if x.Name == name {
			return x
		}
	}
	t.Fatalf("no durable %q in %+v", name, d.List)
	return mail.Durable{}
}

// Durables reports each consumer's own counts, ack floor, filters and last ack,
// and the broker's clock from the consumer info, never the host's.
func TestDurablesReportsCountsFloorFiltersAndTheBrokerClock(t *testing.T) {
	b := newScratchBroker(t)
	for i := 0; i < 5; i++ {
		b.publish(subjA)
	}
	c := b.consumer("mcp_alpha_i1", subjA)
	b.take(c, 3, 0, 1) // two acked in order, one held

	got, err := newQueue(b).Durables(context.Background(), testStream)
	if err != nil {
		t.Fatal(err)
	}
	d := durableByName(t, got, "mcp_alpha_i1")
	if d.Pending != 2 || d.AckPending != 1 || d.AckFloor != 2 {
		t.Errorf("pending %d, ack pending %d, ack floor %d, want 2, 1, 2", d.Pending, d.AckPending, d.AckFloor)
	}
	if len(d.Filters) != 1 || d.Filters[0] != subjA {
		t.Errorf("filters = %v, want [%s]", d.Filters, subjA)
	}
	if d.LastAck.IsZero() {
		t.Error("last ack is zero after two acks")
	}
	if got.At.IsZero() || time.Since(got.At) > time.Minute || got.At.Before(d.LastAck) {
		t.Errorf("broker clock = %v, want the server's time at the answer, not before the last ack %v", got.At, d.LastAck)
	}
}

// A consumer with a filter set reads all of it, and a consumer that never acked
// has no last ack.
func TestDurablesReportsAFilterSetAndNoLastAckBeforeTheFirstAck(t *testing.T) {
	b := newScratchBroker(t)
	b.publish(subjA)
	b.consumer("mcp_alpha_i2", subjA, subjB)

	got, err := newQueue(b).Durables(context.Background(), testStream)
	if err != nil {
		t.Fatal(err)
	}
	d := durableByName(t, got, "mcp_alpha_i2")
	if len(d.Filters) != 2 || d.Filters[0] != subjA || d.Filters[1] != subjB {
		t.Errorf("filters = %v, want both subjects in order", d.Filters)
	}
	if !d.LastAck.IsZero() {
		t.Errorf("last ack = %v, want zero before any ack", d.LastAck)
	}
	if d.AckFloor != 0 || d.Pending != 1 {
		t.Errorf("floor %d pending %d, want 0 and 1", d.AckFloor, d.Pending)
	}
}

// A stream that does not exist is an error, never an empty list: SeatMail reads
// an error as unknown and an empty list as no durable.
func TestDurablesOnAMissingStreamIsAnErrorNotAnEmptyList(t *testing.T) {
	b := newScratchBroker(t)
	_, err := newQueue(b).Durables(context.Background(), "NO_SUCH_STREAM")
	if err == nil {
		t.Fatal("Durables on a missing stream returned no error")
	}
}

// Oldest returns the first message above a sequence on any of the subjects,
// with the server's stored time, and skips messages on other subjects.
func TestOldestIsTheFirstAboveTheSequenceOnTheGivenSubjectsOnly(t *testing.T) {
	b := newScratchBroker(t)
	b.publish(subjOther) // 1
	b.publish(subjA)     // 2
	b.publish(subjOther) // 3
	b.publish(subjB)     // 4
	b.publish(subjA)     // 5
	q := newQueue(b)

	cases := []struct {
		name     string
		above    uint64
		subjects []string
		want     uint64
		found    bool
	}{
		{"first on one subject", 0, []string{subjA}, 2, true},
		{"above the first hit", 2, []string{subjA}, 5, true},
		{"the earlier of two subjects", 0, []string{subjB, subjA}, 2, true},
		{"the other subject when the first has none above", 2, []string{subjA, subjB}, 4, true},
		{"above is exclusive", 5, []string{subjA}, 0, false},
		{"no message on the subject", 0, []string{"agent.aae.squad.nobody.inbox"}, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, ok, err := q.Oldest(context.Background(), testStream, c.above, c.subjects)
			if err != nil {
				t.Fatal(err)
			}
			if ok != c.found || m.Seq != c.want {
				t.Errorf("Oldest = (%d, %v), want (%d, %v)", m.Seq, ok, c.want, c.found)
			}
			if ok && (m.Stored.IsZero() || time.Since(m.Stored) > time.Minute) {
				t.Errorf("stored time = %v, want the server's time of storage", m.Stored)
			}
		})
	}
}

// Count is the stream's own count on the subjects, summed, and zero for a
// subject nothing was published on.
func TestCountSumsTheStreamsCountOnTheSubjects(t *testing.T) {
	b := newScratchBroker(t)
	for i := 0; i < 3; i++ {
		b.publish(subjA)
	}
	b.publish(subjB)
	b.publish(subjOther)
	q := newQueue(b)

	for _, c := range []struct {
		name     string
		subjects []string
		want     int64
	}{
		{"one subject", []string{subjA}, 3},
		{"two subjects", []string{subjA, subjB}, 4},
		{"none published", []string{"agent.aae.squad.nobody.inbox"}, 0},
		{"a repeated subject counts once", []string{subjA, subjA}, 3},
	} {
		got, err := q.Count(context.Background(), testStream, c.subjects)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: Count = %d, want %d", c.name, got, c.want)
		}
	}
}

// A durable resumed past older mail starts from its own start sequence. What the
// broker reports as its ack floor decides whether the first unread message is
// found "above the floor" or below where the durable began; this pins what the
// server reports, so Oldest's caller never reads mail the durable skipped.
func TestAResumedDurableReportsAFloorBelowItsStartSequence(t *testing.T) {
	b := newScratchBroker(t)
	for i := 0; i < 5; i++ {
		b.publish(subjA)
	}
	_, err := b.js.CreateOrUpdateConsumer(context.Background(), testStream, njs.ConsumerConfig{
		Durable: "mcp_alpha_i3", AckPolicy: njs.AckExplicitPolicy, FilterSubject: subjA,
		DeliverPolicy: njs.DeliverByStartSequencePolicy, OptStartSeq: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	q := newQueue(b)
	got, err := q.Durables(context.Background(), testStream)
	if err != nil {
		t.Fatal(err)
	}
	d := durableByName(t, got, "mcp_alpha_i3")
	t.Logf("resumed at 4: pending %d, ack pending %d, ack floor %d", d.Pending, d.AckPending, d.AckFloor)
	m, ok, err := q.Oldest(context.Background(), testStream, d.AckFloor, d.Filters)
	if err != nil || !ok {
		t.Fatalf("Oldest above %d: %v %v", d.AckFloor, ok, err)
	}
	if d.Pending != 2 || m.Seq != 4 {
		t.Errorf("pending %d and first unread %d, want 2 and 4 (the durable resumed at 4)", d.Pending, m.Seq)
	}
}
