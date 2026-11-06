// Package mail defines what the watcher knows about a seat's unread mail and
// the interface that reads it (docs/design/marvel-watcher.md sections 3, 11.2
// and 11.3).
//
// Every figure is a value or a stated reason it is not one. A reading nobody
// set is unknown, never zero, so an unset field cannot pass for "no mail".
//
// This package imports no NATS. The JetStream adapter lives in its own package
// and is handed in as a MailReader; a source test holds the boundary.
package mail

import (
	"context"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// Kind says what a reading holds. The zero value is KindUnknown, so a field
// that was never set reads unknown.
type Kind uint8

const (
	// KindUnknown: the read was tried and failed, or never happened.
	KindUnknown Kind = iota
	// KindValue: the reading holds a value.
	KindValue
	// KindUnsupported: the queue cannot answer this question.
	KindUnsupported
	// KindNever: the event has not happened. Only a Time can read it.
	KindNever
)

func (k Kind) String() string {
	switch k {
	case KindValue:
		return "value"
	case KindUnsupported:
		return "unsupported"
	case KindNever:
		return "never"
	default:
		return "unknown"
	}
}

// cell is a value of T or a Kind that says why there is none.
type cell[T any] struct {
	kind Kind
	v    T
}

// Kind reports what the reading holds.
func (c cell[T]) Kind() Kind { return c.kind }

// Value returns the value and true only for KindValue. For any other kind it
// returns the zero T and false, so a caller cannot read a state as a number.
func (c cell[T]) Value() (T, bool) {
	if c.kind != KindValue {
		var zero T
		return zero, false
	}
	return c.v, true
}

// Count is a number of messages.
type Count struct{ cell[int64] }

// Duration is a span of time.
type Duration struct{ cell[time.Duration] }

// Time is an instant, and the only reading that can be KindNever.
type Time struct{ cell[time.Time] }

// Seq is a stream sequence number. 0 is no message: stream sequences start at 1.
type Seq struct{ cell[uint64] }

// CountOf holds n. A negative count is not a reading and is left unknown.
func CountOf(n int64) Count {
	if n < 0 {
		return Count{}
	}
	return Count{cell[int64]{KindValue, n}}
}

// DurationOf holds d. A negative span is not a reading and is left unknown.
func DurationOf(d time.Duration) Duration {
	if d < 0 {
		return Duration{}
	}
	return Duration{cell[time.Duration]{KindValue, d}}
}

// TimeOf holds t.
func TimeOf(t time.Time) Time { return Time{cell[time.Time]{KindValue, t}} }

// SeqOf holds n.
func SeqOf(n uint64) Seq { return Seq{cell[uint64]{KindValue, n}} }

// UnsupportedCount is a Count the queue cannot answer.
func UnsupportedCount() Count { return Count{cell[int64]{kind: KindUnsupported}} }

// UnsupportedDuration is a Duration the queue cannot answer.
func UnsupportedDuration() Duration { return Duration{cell[time.Duration]{kind: KindUnsupported}} }

// UnsupportedTime is a Time the queue cannot answer.
func UnsupportedTime() Time { return Time{cell[time.Time]{kind: KindUnsupported}} }

// UnsupportedSeq is a Seq the queue cannot answer.
func UnsupportedSeq() Seq { return Seq{cell[uint64]{kind: KindUnsupported}} }

// NeverTime is a Time for an event that has not happened, such as the last
// ack of a seat that never acked.
func NeverTime() Time { return Time{cell[time.Time]{kind: KindNever}} }

// MailState is one read of a seat's unread mail. Each reading is a value,
// unknown (tried and failed) or unsupported (the queue cannot answer); never a
// zero by default.
type MailState struct {
	// Depth is the unread count, shown and never a trigger: redelivery inflates it.
	Depth Count
	// OldestAge is the trigger, on the broker's clock (section 11.2).
	OldestAge Duration
	// LastAckAt is a value, never, unknown or unsupported.
	LastAckAt Time
	// ObservedAt is when the read was taken.
	ObservedAt time.Time
	// Source names the adapter.
	Source string
	// Note is the adapter's own note, for example "reads outside durable".
	Note string

	// AckPending, AckFloor and OldestSeq let a parity fixture compare the
	// adapter against the reader it stands in for (section 11.3).
	AckPending Count
	AckFloor   Seq
	OldestSeq  Seq
}

// UnknownState is the state a reader returns beside an error: every reading
// unknown, the source named, and the cause in Note. A reader never returns a
// zero count in place of a failed read.
func UnknownState(source string, at time.Time, err error) MailState {
	s := MailState{Source: source, ObservedAt: at}
	if err != nil {
		s.Note = err.Error()
	}
	return s
}

// MailReader reads one seat's mail. An error returns beside UnknownState.
type MailReader interface {
	Read(ctx context.Context, seat api.Session) (MailState, error)
}
