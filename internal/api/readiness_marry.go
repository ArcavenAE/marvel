package api

import (
	"errors"
	"fmt"
	"time"

	"github.com/arcavenae/marvel/internal/asof"
)

// The idle scheduler's marrying step (idle-scheduler design v2, sections 3.1
// to 3.3; aae-orc-oprad). Marry pairs ready items with free seats and returns
// the leases a delivery step would write ahead of its send. It is shadow only:
// it sends nothing, writes nothing and reads no bd, so every input arrives as
// an argument. The readiness check for a seat is RouteSeat, so the matcher and
// the router stay one routing function.
//
// Three choices are not ruled yet (cards 1 to 3): the delivery target, the
// trigger and where an item's work class comes from. They are inputs
// (MarryRulings), never constants.

// MarryScopeClient is the item scope that keeps an item out of the matcher.
// Such an item is counted and nothing else of it is read (ADR-009).
const MarryScopeClient = "client"

// Availability is a seat's state in the availability table. Only free pairs.
type Availability string

const (
	SeatFree    Availability = "free"
	SeatWorking Availability = "working"
	SeatQueued  Availability = "queued"
	SeatParked  Availability = "parked"
	SeatUnknown Availability = "unknown"
)

// LeaseState is where a lease stands in the ledger.
type LeaseState string

const (
	LeaseOpen     LeaseState = "open"     // written ahead of the send, no reply yet
	LeaseTaken    LeaseState = "taken"    // the seat answered TAKE and got GO
	LeaseDeclined LeaseState = "declined" // the seat answered DECLINE
	LeaseReleased LeaseState = "released" // the seat answered RELEASE
	LeaseRefused  LeaseState = "refused"  // cancelled by the re-read before send
)

// Lease is one row of the lease ledger, keyed on the bd item id. The ledger
// is the holder of record; bd is advisory.
type Lease struct {
	ID         string     `json:"id"`
	Item       string     `json:"item"`
	Seat       string     `json:"seat"`
	ValidUntil time.Time  `json:"valid_until"`
	State      LeaseState `json:"state"`
}

// holds reports whether the lease still holds its item and seat at now. A
// lease lapses at its stated valid-until and on no other clock.
func (l Lease) holds(now time.Time) bool {
	return false // red: stub
}

// MarryItem is one ready item with its eligibility record and the bd fields
// the matcher compares, as read for this run.
type MarryItem struct {
	ID string `json:"id"`
	// Scope is the item's scope label; MarryScopeClient keeps it out.
	Scope    string    `json:"scope,omitempty"`
	Priority int       `json:"priority"`
	ReadyAt  time.Time `json:"ready_at"`
	// Classes is the item's work class by source; MarryRulings names the
	// source the matcher reads (card 3).
	Classes      map[string]string `json:"classes,omitempty"`
	Component    string            `json:"component,omitempty"`
	OperatorOnly bool              `json:"operator_only,omitempty"`
	// Duties go to RouteSeat as the duties the job needs.
	Duties   []string `json:"duties,omitempty"`
	Status   string   `json:"status"`
	Assignee string   `json:"assignee,omitempty"`
}

// MarrySeat is one seat's row of the availability table.
type MarrySeat struct {
	Address      string       `json:"address"`
	Team         string       `json:"team"`
	Cluster      string       `json:"cluster"`
	Availability Availability `json:"availability"`
	FreeSince    time.Time    `json:"free_since,omitzero"`
	// Gaps names every input the availability table could not read for this
	// seat, such as its durable consumer or wake path. Any gap keeps the seat
	// from pairing.
	Gaps    []string `json:"gaps,omitempty"`
	Classes []string `json:"classes,omitempty"`
	// Scopes are the components the seat covers, in scope order.
	Scopes []string `json:"scopes,omitempty"`
	// Cells are the seat's readiness cells, read by RouteSeat.
	Cells map[string]asof.Cell[string] `json:"cells,omitempty"`
}

// MarryStop is the stop file: the operator's stop word, seats marked not
// matchable, and paused teams.
type MarryStop struct {
	Stop         bool     `json:"stop,omitempty"`
	NotMatchable []string `json:"not_matchable,omitempty"`
	PausedTeams  []string `json:"paused_teams,omitempty"`
}

// MarryPlacement says which seats a sender may reach: its own cluster, and
// the clusters a placement amendment (B5) has ruled. None are ruled yet.
type MarryPlacement struct {
	SenderCluster string   `json:"sender_cluster"`
	RuledClusters []string `json:"ruled_clusters,omitempty"`
}

// MarryRulings are the open cards, passed in so that no answer is assumed.
type MarryRulings struct {
	DeliveryTarget  string `json:"delivery_target,omitempty"`   // card 1
	Trigger         string `json:"trigger,omitempty"`           // card 2
	WorkClassSource string `json:"work_class_source,omitempty"` // card 3
}

// MarryInput is everything one run reads.
type MarryInput struct {
	RunID     string         `json:"run_id"`
	Now       time.Time      `json:"now"`
	Items     []MarryItem    `json:"items,omitempty"`
	Seats     []MarrySeat    `json:"seats,omitempty"`
	Ledger    []Lease        `json:"ledger,omitempty"`
	Stop      MarryStop      `json:"stop"`
	Placement MarryPlacement `json:"placement"`
	Rulings   MarryRulings   `json:"rulings"`
	// LeaseCap is the most leases one run writes: 1 in the first week live,
	// then 3.
	LeaseCap     int `json:"lease_cap"`
	LeaseMinutes int `json:"lease_minutes"`
}

// UnmatchedKind is why an item found no seat.
type UnmatchedKind string

const (
	UnmatchedNoClass        UnmatchedKind = "no-class"
	UnmatchedNoComponent    UnmatchedKind = "no-component"
	UnmatchedNoEligibleSeat UnmatchedKind = "no-eligible-seat"
	UnmatchedOperatorOnly   UnmatchedKind = "operator-only"
)

// MarryPair is a would-pair row: the item, the seat and the lease that a
// delivery step would write ahead of its send.
type MarryPair struct {
	Item  string `json:"item"`
	Seat  string `json:"seat"`
	Lease string `json:"lease"`
}

// MarryUnplaced is a pair whose seat no ruled sender reaches.
type MarryUnplaced struct {
	Item   string `json:"item"`
	Seat   string `json:"seat"`
	Reason string `json:"reason"`
}

// MarryUnmatched is an item with no seat, by kind.
type MarryUnmatched struct {
	Item string        `json:"item"`
	Kind UnmatchedKind `json:"kind"`
}

// MarryRefusedSeat is a seat the matcher will not address.
type MarryRefusedSeat struct {
	Seat   string `json:"seat"`
	Reason string `json:"reason"`
}

// MarryDuplicate is an item whose bd assignee is not the ledger's holder.
type MarryDuplicate struct {
	Item     string `json:"item"`
	Assignee string `json:"assignee"`
	Holder   string `json:"holder,omitempty"`
}

// MarryCount is the run's count line (section 3.3). Client is the one number
// printed for client-scope items. Held is items a current lease already
// holds. Seats are counted by their stated availability, except that a seat
// with a gap counts as unknown, since its free state is not proven.
type MarryCount struct {
	Ready          int `json:"ready,omitempty"`
	Held           int `json:"held,omitempty"`
	Eligible       int `json:"eligible,omitempty"`
	Unmatched      int `json:"unmatched,omitempty"`
	NoClass        int `json:"no_class,omitempty"`
	NoComponent    int `json:"no_component,omitempty"`
	NoEligibleSeat int `json:"no_eligible_seat,omitempty"`
	OperatorOnly   int `json:"operator_only,omitempty"`
	Client         int `json:"client,omitempty"`
	Free           int `json:"free,omitempty"`
	Working        int `json:"working,omitempty"`
	Queued         int `json:"queued,omitempty"`
	Parked         int `json:"parked,omitempty"`
	Unknown        int `json:"unknown,omitempty"`
	WouldPair      int `json:"would_pair,omitempty"`
	LeasesSent     int `json:"leases_sent,omitempty"`
	Taken          int `json:"taken,omitempty"`
	Declined       int `json:"declined,omitempty"`
	Lapsed         int `json:"lapsed,omitempty"`
	Duplicate      int `json:"duplicate,omitempty"`
}

// String is the count line. Leases sent is always 0 here: Marry is shadow
// only, and would-pair is what it has.
func (c MarryCount) String() string {
	return fmt.Sprintf("ready %d, held %d, eligible %d, unmatched %d (no class %d, no component %d, no eligible seat %d, operator-only %d), client %d; "+
		"seats free %d, working %d, queued %d, parked %d, unknown %d; "+
		"would-pair %d, leases sent %d, taken %d, declined %d, lapsed %d, DUPLICATE %d",
		c.Ready, c.Held, c.Eligible, c.Unmatched, c.NoClass, c.NoComponent, c.NoEligibleSeat, c.OperatorOnly, c.Client,
		c.Free, c.Working, c.Queued, c.Parked, c.Unknown,
		c.WouldPair, c.LeasesSent, c.Taken, c.Declined, c.Lapsed, c.Duplicate)
}

// MarryOutput is one run's result.
type MarryOutput struct {
	Stopped    bool               `json:"stopped,omitempty"`
	Pairs      []MarryPair        `json:"pairs,omitempty"`
	Leases     []Lease            `json:"leases,omitempty"`
	Unplaced   []MarryUnplaced    `json:"unplaced,omitempty"`
	Unmatched  []MarryUnmatched   `json:"unmatched,omitempty"`
	Refused    []MarryRefusedSeat `json:"refused,omitempty"`
	Duplicates []MarryDuplicate   `json:"duplicates,omitempty"`
	Count      MarryCount         `json:"count"`
}

// UnplacedNoSender is the reason a pair prints when its seat is on a cluster
// no ruled sender reaches.
const UnplacedNoSender = "no sender ruled"

// MarryAddress accepts only an agent:// seat address. No path through the
// matcher builds a role://, broadcast:// or global:// address, so nothing it
// produces can fan out.
func MarryAddress(addr string) error {
	return errors.New("red: stub")
}

// Marry runs the marrying step over one snapshot of its inputs. Items go in
// priority order, then by age; for each, the eligible free seats go in scope
// order, then longest free (R-116). It returns an error only for an input it
// cannot run on.
func Marry(in MarryInput) (MarryOutput, error) {
	return MarryOutput{}, nil // red: stub
}

// candidate is a seat that passed the eligibility join for an item, with the
// item's component's position in the seat's scope order.
type candidate struct {
	seat MarrySeat
	rank int
}

// eligibleSeats is the eligibility join for one item: the seat takes the
// work class, covers the component, has an agent:// address and RouteSeat
// finds it eligible for every duty. Scope filters here, before any ordering.
func eligibleSeats(in MarryInput, addressable []bool, it MarryItem, class string) []candidate {
	return nil // red: stub
}

// strictlyFree: only a seat the table reads as free, with no gap, not marked
// not matchable and not in a paused team, may pair. Unknown is never free.
func strictlyFree(in MarryInput, s MarrySeat) bool {
	return false // red: stub
}

// placed reports whether a ruled sender reaches the seat.
func placed(p MarryPlacement, s MarrySeat) bool {
	return false // red: stub
}

// SendReRead is what the delivery step reads again just before a send: the
// seat's row, the item's bd status and assignee, whether an open PR names the
// item, and the stop file.
type SendReRead struct {
	Now          time.Time `json:"now"`
	Seat         MarrySeat `json:"seat"`
	ItemStatus   string    `json:"item_status"`
	ItemAssignee string    `json:"item_assignee,omitempty"`
	OverlapPR    bool      `json:"overlap_pr,omitempty"`
	Stop         MarryStop `json:"stop"`
}

// SendCheck is the re-read's answer. A refusal cancels the lease as refused
// at send.
type SendCheck struct {
	Send   bool   `json:"send"`
	Reason string `json:"reason,omitempty"`
}

// RecheckBeforeSend is the last check before a send. Any flip since the run
// paired the item refuses the send.
func RecheckBeforeSend(l Lease, rr SendReRead) SendCheck {
	return SendCheck{} // red: stub
}

// Answers to a seat's TAKE.
const (
	AnswerGo         = "GO"
	AnswerLapsed     = "LAPSED"
	AnswerNotCurrent = "NOT-CURRENT"
)

// AnswerTake is the matcher's reply to TAKE <lease>: GO only while the lease
// is open and before its valid-until, LAPSED at or after it, and NOT-CURRENT
// for a lease already taken, declined, released or refused.
func AnswerTake(l Lease, now time.Time) string {
	return "" // red: stub
}
