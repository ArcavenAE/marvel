package mail

import "time"

// Tier names the stream a row was read from.
type Tier string

const (
	TierLocal  Tier = "local"
	TierGlobal Tier = "global"
)

// DurableRow is one durable's reading: what SeatMail read before it combined
// anything, so the parity fixture compares each durable on its own.
type DurableRow struct {
	Tier Tier
	// Name is empty for a seat with no durable.
	Name string
	// Live is true for a durable a session holds; false for the departed
	// durable that supplies an inherited floor, and for the unnamed row.
	Live                bool
	Pending, AckPending int64
	AckFloor            uint64
	// OldestSeq is the first unread message's sequence and OldestStored its
	// stored time; both are zero when nothing is unread.
	OldestSeq    uint64
	OldestStored time.Time
}
