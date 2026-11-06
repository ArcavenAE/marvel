package mail

import (
	"context"
	"time"
)

// Queue is the least a mail queue must answer for SeatMail to compute a seat's
// state: which consumers it has and where each has acked up to, the first
// message after a point, and a count of messages. The JetStream adapter
// implements it over the broker (A3); mailtest.Queue implements it in memory,
// which shows the reading does not depend on NATS.
//
// A stream is one tier's inbox, named by the caller. Subjects are exact
// subjects; a filter set is the subjects one consumer reads.
type Queue interface {
	// Durables lists the consumers of stream, each with the counts the broker
	// holds for it, and the broker's own clock at the moment it answered.
	Durables(ctx context.Context, stream string) (Durables, error)
	// Oldest returns the first message above seq on any of subjects, and false
	// when there is none.
	Oldest(ctx context.Context, stream string, above uint64, subjects []string) (Msg, bool, error)
	// Count returns how many messages the stream holds on any of subjects.
	Count(ctx context.Context, stream string, subjects []string) (int64, error)
}

// Durables is one answer to Queue.Durables.
type Durables struct {
	// At is the broker's clock when it answered. Zero means the broker did not
	// say, and an age then runs on the reader's clock (section 11.2).
	At   time.Time
	List []Durable
}

// Durable is one consumer.
type Durable struct {
	// Name is the consumer's name, mcp_<agent id>_<instance> for a seat's.
	Name string
	// Filters are the subjects it reads.
	Filters []string
	// Live is true while a session holds it.
	Live bool
	// Pending and AckPending are the broker's counts: messages not yet
	// delivered to it, and delivered but not acked.
	Pending, AckPending int64
	// AckFloor is the stream sequence it has acked up to.
	AckFloor uint64
	// LastAck is when it last acked; zero means it never has.
	LastAck time.Time
}

// Msg is one stored message.
type Msg struct {
	Seq    uint64
	Stored time.Time
}
