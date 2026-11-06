package mail

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// The local tier's stream and the prefixes of the durable names a seat's mail
// shim creates (docs/design/marvel-watcher.md section 11.3, A2). The agent id
// is split from the instance at the last underscore, as director does.
const (
	localStream   = "AGENT_INBOX"
	localPrefix   = "mcp_"
	globalPrefix  = "mcp_global_"
	seatMailTag   = "seatmail"
	readersClock  = "age on the reader's clock"
	noGlobalNote  = "no global address"
	noLiveNote    = "no live durable"
	noDurableNote = "no durable"
)

// subjectToken is the address schema's character class for one subject
// segment. A name outside it could carry a wildcard or a separator into a
// filter, so it is refused.
var subjectToken = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// GlobalAddr is a seat's address on the global tier: the stream that holds it
// and the one subject it reads there.
type GlobalAddr struct {
	Stream  string
	Subject string
}

// SupervisorGlobal is a cluster supervisor's global address.
func SupervisorGlobal(cluster string) *GlobalAddr {
	return &GlobalAddr{Stream: "GLOBAL_TO_" + cluster, Subject: "global." + cluster + ".supervisor.inbox"}
}

// DirectorGlobal is the director's global address.
func DirectorGlobal() *GlobalAddr {
	return &GlobalAddr{Stream: "GLOBAL_TO_DIRECTOR", Subject: "global.director.inbox"}
}

// SeatMail computes a seat's MailState from a Queue, to the definitions of
// section 11.2: unread is mail not acked, read per durable from that durable's
// own floor, and age is on the broker's clock.
type SeatMail struct {
	Queue Queue
	// Global returns the seat's global address, or nil for a seat that has
	// none. A nil Global means no seat has one. It is the daemon's wiring that
	// knows which seats hold a global role.
	Global func(api.Session) *GlobalAddr
	// Now is the reader's own clock, used for ObservedAt and, when the broker
	// does not stamp its answers, for the age. Nil reads time.Now.
	Now func() time.Time
}

func (s *SeatMail) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// tier is one tier's reading of a seat.
type tier struct {
	depth, ackPending int64
	age               time.Duration
	oldestSeq, floor  uint64
	lastAck           time.Time
	note              string
}

// Read implements MailReader. A tier that cannot be read makes the whole seat
// unknown and returns its error; a seat with no global address reads that tier
// unsupported and is read on the local tier alone.
func (s *SeatMail) Read(ctx context.Context, seat api.Session) (MailState, error) {
	observed := s.now()
	if err := ctx.Err(); err != nil {
		return UnknownState(seatMailTag, observed, err), err
	}
	filters, err := localFilters(seat)
	if err != nil {
		return UnknownState(seatMailTag, observed, err), err
	}

	var notes []string
	local, err := s.readTier(ctx, localStream, localPrefix, seat.Name, filters)
	if err != nil {
		err = fmt.Errorf("local tier: %w", err)
		return UnknownState(seatMailTag, observed, err), err
	}
	tiers := []tier{local}
	if local.note != "" {
		notes = append(notes, "local: "+local.note)
	}

	var g *GlobalAddr
	if s.Global != nil {
		g = s.Global(seat)
	}
	if g == nil {
		notes = append(notes, "global: "+noGlobalNote)
	} else {
		gt, err := s.readTier(ctx, g.Stream, globalPrefix, seat.Name, []string{g.Subject})
		if err != nil {
			err = fmt.Errorf("global tier: %w", err)
			return UnknownState(seatMailTag, observed, err), err
		}
		tiers = append(tiers, gt)
		if gt.note != "" {
			notes = append(notes, "global: "+gt.note)
		}
	}

	return aggregate(tiers, observed, strings.Join(notes, "; ")), nil
}

// localFilters is the filter set a seat's local durable reads: its own inbox,
// and its role's inbox when it holds a role.
func localFilters(seat api.Session) ([]string, error) {
	toks := []struct{ name, v string }{{"workspace", seat.Workspace}, {"team", seat.Team}, {"agent id", seat.Name}}
	if seat.Role != "" {
		toks = append(toks, struct{ name, v string }{"role", seat.Role})
	}
	for _, t := range toks {
		if !subjectToken.MatchString(t.v) {
			return nil, fmt.Errorf("%s %q is not an addressable name", t.name, t.v)
		}
	}
	out := []string{"agent." + seat.Workspace + "." + seat.Team + "." + seat.Name + ".inbox"}
	if seat.Role != "" {
		out = append(out, "agent."+seat.Workspace+"."+seat.Team+".role."+seat.Role+".inbox")
	}
	return out, nil
}

// durableID is the agent id of a durable name under prefix: the text before
// its last underscore. ok is false for a name that is not of that form.
func durableID(name, prefix string) (string, bool) {
	rest, ok := strings.CutPrefix(name, prefix)
	if !ok {
		return "", false
	}
	i := strings.LastIndex(rest, "_")
	if i <= 0 {
		return "", false
	}
	return rest[:i], true
}

func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// readTier reads one tier of one seat.
func (s *SeatMail) readTier(ctx context.Context, stream, prefix, id string, filters []string) (tier, error) {
	ds, err := s.Queue.Durables(ctx, stream)
	if err != nil {
		return tier{}, err
	}
	at, note := ds.At, ""
	if at.IsZero() {
		at, note = s.now(), readersClock
	}

	var live, departed []Durable
	for _, d := range ds.List {
		did, ok := durableID(d.Name, prefix)
		if !ok || did != id || !sameSet(d.Filters, filters) {
			continue
		}
		if d.Live {
			live = append(live, d)
		} else {
			departed = append(departed, d)
		}
	}

	switch {
	case len(live) > 0:
		return s.readDurables(ctx, stream, filters, at, note, live)
	case len(departed) > 0:
		// A successor resumes where its predecessor left off, so the departed
		// durable that acked furthest holds the floor and supplies the counts.
		best := departed[0]
		for _, d := range departed[1:] {
			if d.AckFloor > best.AckFloor || (d.AckFloor == best.AckFloor && d.LastAck.After(best.LastAck)) {
				best = d
			}
		}
		return s.readDurables(ctx, stream, filters, at, join(note, noLiveNote), []Durable{best})
	default:
		// No durable at all: a successor would read everything the inbox holds
		// on the seat's subjects, from floor 0.
		n, err := s.Queue.Count(ctx, stream, filters)
		if err != nil {
			return tier{}, err
		}
		t := tier{depth: n, note: join(note, noDurableNote)}
		first, found, err := s.Queue.Oldest(ctx, stream, 0, filters)
		if err != nil {
			return tier{}, err
		}
		if found {
			t.oldestSeq, t.age = first.Seq, ageAt(at, first.Stored)
		}
		return t, nil
	}
}

// readDurables reads each durable from its own floor and takes, within the
// tier, the maximum depth and age: two live sessions of one seat each hold
// their own copy of the mail, so their counts are never summed and their
// floors never merged.
func (s *SeatMail) readDurables(ctx context.Context, stream string, filters []string, at time.Time, note string, ds []Durable) (tier, error) {
	t := tier{note: note}
	for _, d := range ds {
		depth := d.Pending + d.AckPending
		first, found, err := s.Queue.Oldest(ctx, stream, d.AckFloor, filters)
		if err != nil {
			return tier{}, err
		}
		t.depth = max(t.depth, depth)
		t.ackPending = max(t.ackPending, d.AckPending)
		t.floor = max(t.floor, d.AckFloor)
		if d.LastAck.After(t.lastAck) {
			t.lastAck = d.LastAck
		}
		if found {
			t.age = max(t.age, ageAt(at, first.Stored))
			if t.oldestSeq == 0 || first.Seq < t.oldestSeq {
				t.oldestSeq = first.Seq
			}
		}
	}
	return t, nil
}

func ageAt(at, stored time.Time) time.Duration {
	if d := at.Sub(stored); d > 0 {
		return d
	}
	return 0
}

func join(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

// aggregate combines the tiers that were read: depth is summed and the oldest
// age is the maximum. The two tiers are two streams, whose sequence numbers
// do not compare, so with both counted the floor and the oldest sequence are
// unsupported; with the local tier alone they are its own.
func aggregate(tiers []tier, observed time.Time, note string) MailState {
	var depth, ackPending int64
	var age time.Duration
	var lastAck time.Time
	for _, t := range tiers {
		depth += t.depth
		ackPending += t.ackPending
		age = max(age, t.age)
		if t.lastAck.After(lastAck) {
			lastAck = t.lastAck
		}
	}
	st := MailState{
		Depth:      CountOf(depth),
		AckPending: CountOf(ackPending),
		OldestAge:  DurationOf(age),
		LastAckAt:  NeverTime(),
		ObservedAt: observed,
		Source:     seatMailTag,
		Note:       note,
	}
	if !lastAck.IsZero() {
		st.LastAckAt = TimeOf(lastAck)
	}
	if len(tiers) == 1 {
		st.AckFloor = SeqOf(tiers[0].floor)
		st.OldestSeq = SeqOf(tiers[0].oldestSeq)
	} else {
		st.AckFloor = UnsupportedSeq()
		st.OldestSeq = UnsupportedSeq()
	}
	return st
}
