package mailtest

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/mail"
)

// DurableSpec describes a consumer to add to a Queue. Its Pending is worked out
// from the stream: the messages on its filters above its floor, less the
// AckPending count a test gives it.
type DurableSpec struct {
	Name       string
	Filters    []string
	Live       bool
	Floor      uint64
	LastAck    time.Time
	AckPending int64
}

type qmsg struct {
	seq     uint64
	subject string
	stored  time.Time
}

type qstream struct {
	msgs     []qmsg
	durables []DurableSpec
}

// Queue is an in-memory mail.Queue: streams of messages and the consumers that
// read them. It stands in for a broker so SeatMail can be tested without one.
// Filters are exact subjects.
type Queue struct {
	now func() time.Time
	// Unstamped makes the queue leave the broker clock zero in its answers, as
	// a server that does not stamp them would.
	Unstamped bool

	mu      sync.Mutex
	calls   int
	fail    map[int]error
	streams map[string]*qstream
}

var _ mail.Queue = (*Queue)(nil)

// NewQueue returns an empty queue whose broker clock is now.
func NewQueue(t testing.TB, now func() time.Time) *Queue {
	t.Helper()
	return &Queue{now: now, fail: map[int]error{}, streams: map[string]*qstream{}}
}

// AddStream makes an empty stream.
func (q *Queue) AddStream(name string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.streams[name] = &qstream{}
}

// Publish stores a message with the given sequence on subject.
func (q *Queue) Publish(stream, subject string, seq uint64, stored time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	s := q.must(stream)
	s.msgs = append(s.msgs, qmsg{seq, subject, stored})
}

// AddDurable adds a consumer to a stream.
func (q *Queue) AddDurable(stream string, d DurableSpec) {
	q.mu.Lock()
	defer q.mu.Unlock()
	s := q.must(stream)
	s.durables = append(s.durables, d)
}

// FailOn makes the nth call to the queue (counting from 1, across Durables,
// Oldest and Count) return err.
func (q *Queue) FailOn(n int, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.fail[n] = err
}

func (q *Queue) must(stream string) *qstream {
	s, ok := q.streams[stream]
	if !ok {
		panic(fmt.Sprintf("mailtest: no stream %q (AddStream first)", stream))
	}
	return s
}

// begin counts a call and returns the stream, or the error the call must
// return: the context's, an injected one, or no such stream.
func (q *Queue) begin(ctx context.Context, stream string) (*qstream, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.calls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := q.fail[q.calls]; err != nil {
		return nil, err
	}
	s, ok := q.streams[stream]
	if !ok {
		return nil, fmt.Errorf("mailtest: stream %q: no such stream", stream)
	}
	return s, nil
}

func onAny(m qmsg, subjects []string) bool { return slices.Contains(subjects, m.subject) }

// Durables implements mail.Queue.
func (q *Queue) Durables(ctx context.Context, stream string) (mail.Durables, error) {
	s, err := q.begin(ctx, stream)
	if err != nil {
		return mail.Durables{}, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	out := mail.Durables{}
	if !q.Unstamped {
		out.At = q.now()
	}
	for _, d := range s.durables {
		var pending int64
		for _, m := range s.msgs {
			if m.seq > d.Floor && onAny(m, d.Filters) {
				pending++
			}
		}
		pending = max(pending-d.AckPending, 0)
		out.List = append(out.List, mail.Durable{
			Name: d.Name, Filters: slices.Clone(d.Filters), Live: d.Live,
			Pending: pending, AckPending: d.AckPending, AckFloor: d.Floor, LastAck: d.LastAck,
		})
	}
	return out, nil
}

// Oldest implements mail.Queue.
func (q *Queue) Oldest(ctx context.Context, stream string, above uint64, subjects []string) (mail.Msg, bool, error) {
	s, err := q.begin(ctx, stream)
	if err != nil {
		return mail.Msg{}, false, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	var best mail.Msg
	var found bool
	for _, m := range s.msgs {
		if m.seq > above && onAny(m, subjects) && (!found || m.seq < best.Seq) {
			best, found = mail.Msg{Seq: m.seq, Stored: m.stored}, true
		}
	}
	return best, found, nil
}

// Count implements mail.Queue.
func (q *Queue) Count(ctx context.Context, stream string, subjects []string) (int64, error) {
	s, err := q.begin(ctx, stream)
	if err != nil {
		return 0, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	var n int64
	for _, m := range s.msgs {
		if onAny(m, subjects) {
			n++
		}
	}
	return n, nil
}
