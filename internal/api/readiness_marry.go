package api

import (
	"errors"
	"fmt"
	"slices"
	"strings"
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
	return (l.State == LeaseOpen || l.State == LeaseTaken) && now.Before(l.ValidUntil)
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

// MarryAddress accepts only an agent:// seat address of the form
// agent://{team}/{id}, each part in the envelope schema's agent_id class. No
// path through the matcher builds a role://, broadcast:// or global://
// address, and no wildcard passes, so nothing it produces can fan out.
func MarryAddress(addr string) error {
	rest, ok := strings.CutPrefix(addr, "agent://")
	if !ok {
		scheme, _, found := strings.Cut(addr, "://")
		if !found {
			return errors.New("not a seat address")
		}
		return fmt.Errorf("%s:// is not a seat address", scheme)
	}
	if rest == "" {
		return errors.New("agent:// names no seat")
	}
	if !agentAddress.MatchString(addr) {
		return fmt.Errorf("%s is not agent://{team}/{id}", addr)
	}
	return nil
}

// Marry runs the marrying step over one snapshot of its inputs. Items go in
// priority order, then by age; for each, the eligible free seats go in scope
// order, then longest free (R-116). It returns an error only for an input it
// cannot run on.
func Marry(in MarryInput) (MarryOutput, error) {
	switch {
	case in.RunID == "":
		return MarryOutput{}, errors.New("marry: no run id")
	case in.Now.IsZero():
		return MarryOutput{}, errors.New("marry: no clock reading")
	case in.LeaseCap < 0:
		return MarryOutput{}, fmt.Errorf("marry: lease cap %d", in.LeaseCap)
	case in.LeaseMinutes <= 0:
		return MarryOutput{}, fmt.Errorf("marry: lease of %d minutes", in.LeaseMinutes)
	}
	// One item, one holder starts at the input: a repeated item would get
	// two leases with one id, and a repeated seat two items. A client-scope
	// item is skipped here too, so none of its fields is read.
	seenSeat := map[string]bool{}
	for _, s := range in.Seats {
		if seenSeat[s.Address] {
			return MarryOutput{}, fmt.Errorf("marry: seat %s is listed twice", s.Address)
		}
		seenSeat[s.Address] = true
	}
	seenItem := map[string]bool{}
	for _, it := range in.Items {
		if it.Scope == MarryScopeClient {
			continue
		}
		if seenItem[it.ID] {
			return MarryOutput{}, fmt.Errorf("marry: item %s is listed twice", it.ID)
		}
		seenItem[it.ID] = true
	}
	var out MarryOutput
	out.Stopped = in.Stop.Stop
	validUntil := in.Now.Add(time.Duration(in.LeaseMinutes) * time.Minute)

	heldItem := map[string]Lease{}
	heldSeat := map[string]bool{}
	for _, l := range in.Ledger {
		switch {
		case l.holds(in.Now):
			heldItem[l.Item] = l
			heldSeat[l.Seat] = true
		case l.State == LeaseOpen:
			out.Count.Lapsed++
		}
		switch l.State {
		case LeaseTaken:
			out.Count.Taken++
		case LeaseDeclined:
			out.Count.Declined++
		}
	}

	addressable := make([]bool, len(in.Seats))
	for i, s := range in.Seats {
		switch {
		case len(s.Gaps) > 0:
			out.Count.Unknown++
		case s.Availability == SeatFree:
			out.Count.Free++
		case s.Availability == SeatWorking:
			out.Count.Working++
		case s.Availability == SeatQueued:
			out.Count.Queued++
		case s.Availability == SeatParked:
			out.Count.Parked++
		default:
			out.Count.Unknown++
		}
		if err := MarryAddress(s.Address); err != nil {
			out.Refused = append(out.Refused, MarryRefusedSeat{Seat: s.Address, Reason: err.Error()})
			continue
		}
		addressable[i] = true
	}

	// A client-scope item is counted by its scope alone; nothing else of it
	// is read.
	var items []MarryItem
	for _, it := range in.Items {
		if it.Scope == MarryScopeClient {
			out.Count.Client++
			continue
		}
		items = append(items, it)
	}
	slices.SortStableFunc(items, func(a, b MarryItem) int {
		if a.Priority != b.Priority {
			return a.Priority - b.Priority
		}
		if c := a.ReadyAt.Compare(b.ReadyAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	out.Count.Ready = len(items)

	for _, it := range items {
		if it.Assignee == "" {
			continue
		}
		if l, ok := heldItem[it.ID]; !ok || l.Seat != it.Assignee {
			out.Duplicates = append(out.Duplicates, MarryDuplicate{Item: it.ID, Assignee: it.Assignee, Holder: l.Seat})
		}
	}

	used := map[string]bool{}
	for _, it := range items {
		if _, ok := heldItem[it.ID]; ok {
			out.Count.Held++
			continue
		}
		if it.Status != "open" || it.Assignee != "" {
			continue
		}
		class := ""
		if in.Rulings.WorkClassSource != "" {
			class = it.Classes[in.Rulings.WorkClassSource]
		}
		var kind UnmatchedKind
		switch {
		case it.OperatorOnly:
			kind = UnmatchedOperatorOnly
		case class == "":
			kind = UnmatchedNoClass
		case it.Component == "":
			kind = UnmatchedNoComponent
		}
		var cands []candidate
		if kind == "" {
			cands = eligibleSeats(in, addressable, it, class)
			if len(cands) == 0 {
				kind = UnmatchedNoEligibleSeat
			}
		}
		if kind != "" {
			out.Unmatched = append(out.Unmatched, MarryUnmatched{Item: it.ID, Kind: kind})
			out.Count.Unmatched++
			switch kind {
			case UnmatchedOperatorOnly:
				out.Count.OperatorOnly++
			case UnmatchedNoClass:
				out.Count.NoClass++
			case UnmatchedNoComponent:
				out.Count.NoComponent++
			case UnmatchedNoEligibleSeat:
				out.Count.NoEligibleSeat++
			}
			continue
		}
		out.Count.Eligible++
		if out.Stopped || len(out.Leases) >= in.LeaseCap {
			continue
		}
		var free []candidate
		for _, c := range cands {
			if strictlyFree(in, c.seat) && !heldSeat[c.seat.Address] && !used[c.seat.Address] {
				free = append(free, c)
			}
		}
		slices.SortStableFunc(free, func(a, b candidate) int {
			if a.rank != b.rank {
				return a.rank - b.rank
			}
			// A seat with no free-since time is not known to be the longest
			// free, so it goes after every seat that has one.
			if az, bz := a.seat.FreeSince.IsZero(), b.seat.FreeSince.IsZero(); az != bz {
				if az {
					return 1
				}
				return -1
			}
			if c := a.seat.FreeSince.Compare(b.seat.FreeSince); c != 0 {
				return c
			}
			return strings.Compare(a.seat.Address, b.seat.Address)
		})
		placedAt := slices.IndexFunc(free, func(c candidate) bool { return placed(in.Placement, c.seat) })
		switch {
		case placedAt >= 0:
			s := free[placedAt].seat
			used[s.Address] = true
			l := Lease{ID: in.RunID + "/" + it.ID, Item: it.ID, Seat: s.Address, ValidUntil: validUntil, State: LeaseOpen}
			out.Leases = append(out.Leases, l)
			out.Pairs = append(out.Pairs, MarryPair{Item: it.ID, Seat: s.Address, Lease: l.ID})
			out.Count.WouldPair++
		case len(free) > 0:
			out.Unplaced = append(out.Unplaced, MarryUnplaced{Item: it.ID, Seat: free[0].seat.Address, Reason: UnplacedNoSender})
		}
	}
	out.Count.Duplicate = len(out.Duplicates)
	return out, nil
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
	var out []candidate
	for i, s := range in.Seats {
		if !addressable[i] || !slices.Contains(s.Classes, class) {
			continue
		}
		rank := slices.Index(s.Scopes, it.Component)
		if rank < 0 {
			continue
		}
		d := RouteSeat(RouteInput{Seat: s.Address, Duties: it.Duties, Cells: s.Cells}, in.Now)
		if d.Verdict != RouteEligible {
			continue
		}
		out = append(out, candidate{seat: s, rank: rank})
	}
	return out
}

// strictlyFree: only a seat the table reads as free, with no gap, not marked
// not matchable and not in a paused team, may pair. Unknown is never free.
func strictlyFree(in MarryInput, s MarrySeat) bool {
	return s.Availability == SeatFree && len(s.Gaps) == 0 &&
		!slices.Contains(in.Stop.NotMatchable, s.Address) && !slices.Contains(in.Stop.PausedTeams, s.Team)
}

// placed reports whether a ruled sender reaches the seat.
func placed(p MarryPlacement, s MarrySeat) bool {
	return s.Cluster != "" && (s.Cluster == p.SenderCluster || slices.Contains(p.RuledClusters, s.Cluster))
}

// SendReRead is what the delivery step reads again just before a send: the
// seat's row, the item's bd status and assignee, whether an open PR names the
// item, and the stop file.
type SendReRead struct {
	Now          time.Time `json:"now"`
	Seat         MarrySeat `json:"seat"`
	ItemStatus   string    `json:"item_status"`
	ItemAssignee string    `json:"item_assignee,omitempty"`
	// ItemScope is the item's scope label as read again; a flip to client
	// scope refuses the send (ADR-009).
	ItemScope string    `json:"item_scope,omitempty"`
	OverlapPR bool      `json:"overlap_pr,omitempty"`
	Stop      MarryStop `json:"stop"`
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
	refuse := func(why string) SendCheck { return SendCheck{Reason: "refused at send: " + why} }
	switch {
	case rr.Stop.Stop:
		return refuse("stop")
	case slices.Contains(rr.Stop.NotMatchable, l.Seat):
		return refuse("not matchable")
	case slices.Contains(rr.Stop.PausedTeams, rr.Seat.Team):
		return refuse("team paused")
	case !l.holds(rr.Now) || l.State != LeaseOpen:
		return refuse("lease not current")
	case rr.Seat.Address != l.Seat || MarryAddress(rr.Seat.Address) != nil:
		return refuse("seat row does not match the lease")
	case rr.Seat.Availability != SeatFree || len(rr.Seat.Gaps) > 0:
		return refuse("seat not free")
	case rr.ItemScope == MarryScopeClient:
		return refuse("item is client scope")
	case rr.ItemStatus != "open":
		return refuse("item not open")
	case rr.ItemAssignee != "":
		return refuse("item assigned")
	case rr.OverlapPR:
		return refuse("an open PR names the item")
	}
	return SendCheck{Send: true}
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
	switch {
	case !now.Before(l.ValidUntil):
		return AnswerLapsed
	case l.State != LeaseOpen:
		return AnswerNotCurrent
	}
	return AnswerGo
}
