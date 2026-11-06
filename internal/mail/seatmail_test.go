package mail_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/mail"
	"github.com/arcavenae/marvel/internal/mail/mailtest"
)

const (
	inbox     = "agent.aae.arcaven.builder-0.inbox"
	roleInbox = "agent.aae.arcaven.role.builder.inbox"
	stream    = "AGENT_INBOX"
	globalStr = "GLOBAL_TO_kinu"
)

var (
	t0   = time.Date(2026, 11, 6, 4, 0, 0, 0, time.UTC)
	seat = api.Session{Name: "builder-0", Workspace: "aae", Team: "arcaven", Role: "builder"}
)

// fixture is a queue whose broker clock reads t0+1h, with the local stream
// made and the stored times of message n at t0+n minutes.
type fixture struct {
	q *mailtest.Queue
	t *testing.T
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	q := mailtest.NewQueue(t, func() time.Time { return t0.Add(time.Hour) })
	q.AddStream(stream)
	return &fixture{q: q, t: t}
}

// publish stores messages 1..n on subject, message k stored k minutes after t0.
func (f *fixture) publish(subject string, seqs ...uint64) {
	for _, s := range seqs {
		f.q.Publish(stream, subject, s, t0.Add(time.Duration(s)*time.Minute))
	}
}

func (f *fixture) durable(name string, live bool, floor uint64, filters ...string) {
	f.q.AddDurable(stream, mailtest.DurableSpec{Name: name, Live: live, Floor: floor, Filters: filters})
}

// reader is a SeatMail with no global addresses unless the test gives one.
func (f *fixture) reader(global func(api.Session) *mail.GlobalAddr) *mail.SeatMail {
	return &mail.SeatMail{Queue: f.q, Global: global, Now: func() time.Time { return t0.Add(time.Hour) }}
}

func need[T any](t *testing.T, name string, c interface{ Value() (T, bool) }) T {
	t.Helper()
	v, ok := c.Value()
	if !ok {
		t.Fatalf("%s has no value", name)
	}
	return v
}

func read(t *testing.T, r *mail.SeatMail) mail.MailState {
	t.Helper()
	got, err := r.Read(context.Background(), seat)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return got
}

// SeatMail is a MailReader.
var _ mail.MailReader = (*mail.SeatMail)(nil)

// A successor whose departed predecessor acked further reads from the
// predecessor's floor: that is where it resumes (docs/design/marvel-watcher.md
// section 11.2, the inherited floor).
func TestASuccessorReadsFromItsDepartedPredecessorsFloor(t *testing.T) {
	f := newFixture(t)
	f.publish(inbox, 1, 2, 3, 4, 5, 6)
	f.durable("mcp_builder-0_old", false, 4, inbox, roleInbox)

	got := read(t, f.reader(nil))

	if v := need[int64](t, "Depth", got.Depth); v != 2 {
		t.Errorf("Depth = %d, want 2 (messages 5 and 6)", v)
	}
	if v := need[uint64](t, "OldestSeq", got.OldestSeq); v != 5 {
		t.Errorf("OldestSeq = %d, want 5", v)
	}
	if v := need[time.Duration](t, "OldestAge", got.OldestAge); v != 55*time.Minute {
		t.Errorf("OldestAge = %v, want 55m (broker clock t0+1h, message 5 stored t0+5m)", v)
	}
	if v := need[uint64](t, "AckFloor", got.AckFloor); v != 4 {
		t.Errorf("AckFloor = %d, want 4", v)
	}
	if !strings.Contains(got.Note, "no live durable") {
		t.Errorf("Note = %q, want it to say \"no live durable\"", got.Note)
	}
}

// Two live durables of one seat each hold their own copy of the mail. The one
// acked ahead does not hide the other's: the seat reports the larger depth and
// the older age, and the two are never summed or merged into one floor.
func TestTwoLiveDurablesReportTheLargerDepthAndTheOlderAge(t *testing.T) {
	f := newFixture(t)
	f.publish(inbox, 1, 2, 3, 4, 5, 6)
	f.durable("mcp_builder-0_a", true, 2, inbox, roleInbox)
	f.durable("mcp_builder-0_b", true, 5, inbox, roleInbox)

	got := read(t, f.reader(nil))

	if v := need[int64](t, "Depth", got.Depth); v != 4 {
		t.Errorf("Depth = %d, want 4 (the max of 4 and 1, not their sum 5)", v)
	}
	if v := need[time.Duration](t, "OldestAge", got.OldestAge); v != 57*time.Minute {
		t.Errorf("OldestAge = %v, want 57m (message 3, the older durable's)", v)
	}
	if v := need[uint64](t, "OldestSeq", got.OldestSeq); v != 3 {
		t.Errorf("OldestSeq = %d, want 3", v)
	}
}

// A live durable is read from its own floor, never from a departed durable
// that acked further.
func TestALiveDurableIsReadFromItsOwnFloorNotADepartedOnes(t *testing.T) {
	f := newFixture(t)
	f.publish(inbox, 1, 2, 3, 4, 5, 6)
	f.durable("mcp_builder-0_live", true, 2, inbox, roleInbox)
	f.durable("mcp_builder-0_gone", false, 5, inbox, roleInbox)

	got := read(t, f.reader(nil))

	if v := need[int64](t, "Depth", got.Depth); v != 4 {
		t.Errorf("Depth = %d, want 4 (from the live floor 2, not the departed floor 5)", v)
	}
	if v := need[uint64](t, "AckFloor", got.AckFloor); v != 2 {
		t.Errorf("AckFloor = %d, want 2", v)
	}
	if strings.Contains(got.Note, "no live durable") {
		t.Errorf("Note = %q: a live durable exists", got.Note)
	}
}

// Mail on the role subject is the same durable's mail: each message counts
// once, and the oldest is the first message on either subject.
func TestRoleMailOnTheSameDurableIsCountedOnce(t *testing.T) {
	f := newFixture(t)
	f.publish(roleInbox, 1)
	f.publish(inbox, 2)
	f.publish(roleInbox, 3)
	f.durable("mcp_builder-0_a", true, 0, inbox, roleInbox)

	got := read(t, f.reader(nil))

	if v := need[int64](t, "Depth", got.Depth); v != 3 {
		t.Errorf("Depth = %d, want 3", v)
	}
	if v := need[uint64](t, "OldestSeq", got.OldestSeq); v != 1 {
		t.Errorf("OldestSeq = %d, want 1 (the role message)", v)
	}
}

// A durable with a different filter set belongs to another seat that shares
// the id prefix. It is skipped, whatever it holds.
func TestADurableWithOtherFiltersIsSkipped(t *testing.T) {
	f := newFixture(t)
	f.publish(inbox, 1, 2)
	f.publish("agent.aae.arcaven.other.inbox", 3, 4, 5, 6, 7)
	f.durable("mcp_builder-0_a", true, 0, inbox, roleInbox)
	f.durable("mcp_builder-0_b", true, 0, "agent.aae.arcaven.other.inbox")

	got := read(t, f.reader(nil))

	if v := need[int64](t, "Depth", got.Depth); v != 2 {
		t.Errorf("Depth = %d, want 2: the durable with other filters is not this seat's", v)
	}
}

// The durable name splits at its last underscore, so an id that carries an
// underscore is not mistaken for this seat's.
func TestADurableOfAnotherIDWithAnUnderscoreIsSkipped(t *testing.T) {
	f := newFixture(t)
	f.publish(inbox, 1)
	f.durable("mcp_builder-0_a", true, 0, inbox, roleInbox)
	f.durable("mcp_builder-0_x_b", true, 0, inbox, roleInbox)

	got := read(t, f.reader(nil))

	if got.Note != "" && strings.Contains(got.Note, "x") {
		t.Errorf("Note = %q", got.Note)
	}
	if v := need[int64](t, "Depth", got.Depth); v != 1 {
		t.Errorf("Depth = %d, want 1", v)
	}
	// Prove the skip is by id: with only the other id's durable the seat has
	// no durable at all, and reads from floor 0.
	g := newFixture(t)
	g.publish(inbox, 1, 2, 3)
	g.durable("mcp_builder-0_x_b", true, 3, inbox, roleInbox)
	got = read(t, g.reader(nil))
	if v := need[int64](t, "Depth", got.Depth); v != 3 {
		t.Errorf("Depth = %d, want 3: the other id's floor must not count", v)
	}
	if !strings.Contains(got.Note, "no durable") {
		t.Errorf("Note = %q, want \"no durable\"", got.Note)
	}
}

// A seat with no durable at all would, on a successor's first read, take every
// message its inbox holds: depth is the stream's count on its subjects and the
// oldest is the first message on them.
func TestASeatWithNoDurableReadsFromFloorZero(t *testing.T) {
	f := newFixture(t)
	f.publish(inbox, 1, 3)
	f.publish(roleInbox, 2)
	f.publish("agent.aae.arcaven.other.inbox", 4, 5)

	got := read(t, f.reader(nil))

	if v := need[int64](t, "Depth", got.Depth); v != 3 {
		t.Errorf("Depth = %d, want 3 (messages 1, 2 and 3 only)", v)
	}
	if v := need[uint64](t, "OldestSeq", got.OldestSeq); v != 1 {
		t.Errorf("OldestSeq = %d, want 1", v)
	}
	if v := need[uint64](t, "AckFloor", got.AckFloor); v != 0 {
		t.Errorf("AckFloor = %d, want 0", v)
	}
	if k := got.LastAckAt.Kind(); k != mail.KindNever {
		t.Errorf("LastAckAt reads %v, want never", k)
	}
	if !strings.Contains(got.Note, "no durable") || strings.Contains(got.Note, "no live durable") {
		t.Errorf("Note = %q, want \"no durable\"", got.Note)
	}
}

// Nothing unread is a real zero.
func TestNothingUnreadReadsAZeroDepth(t *testing.T) {
	f := newFixture(t)
	f.publish(inbox, 1, 2)
	f.durable("mcp_builder-0_a", true, 2, inbox, roleInbox)

	got := read(t, f.reader(nil))

	if v := need[int64](t, "Depth", got.Depth); v != 0 {
		t.Errorf("Depth = %d, want 0", v)
	}
	if v := need[time.Duration](t, "OldestAge", got.OldestAge); v != 0 {
		t.Errorf("OldestAge = %v, want 0", v)
	}
}

func globalOf(cluster string) func(api.Session) *mail.GlobalAddr {
	return func(api.Session) *mail.GlobalAddr { return mail.SupervisorGlobal(cluster) }
}

// A seat with no global address reads that tier unsupported with the note "no
// global address", leaves it out of the totals, and still reports the local
// tier.
func TestASeatWithNoGlobalAddressStillReportsItsLocalTier(t *testing.T) {
	f := newFixture(t)
	f.publish(inbox, 1, 2)
	f.durable("mcp_builder-0_a", true, 0, inbox, roleInbox)

	got := read(t, f.reader(func(api.Session) *mail.GlobalAddr { return nil }))

	if k := got.Depth.Kind(); k != mail.KindValue {
		t.Fatalf("Depth reads %v, want a value", k)
	}
	if v := need[int64](t, "Depth", got.Depth); v != 2 {
		t.Errorf("Depth = %d, want 2", v)
	}
	if !strings.Contains(got.Note, "no global address") {
		t.Errorf("Note = %q, want it to carry \"no global address\"", got.Note)
	}
}

// Across the two tiers the depth is the sum, the oldest age the maximum and
// the last ack the later of the two.
func TestAcrossTiersDepthSumsAndAgeTakesTheMaximum(t *testing.T) {
	f := newFixture(t)
	f.publish(inbox, 1, 2, 3)
	f.durable("mcp_builder-0_a", true, 1, inbox, roleInbox) // depth 2, oldest message 2
	f.q.AddStream(globalStr)
	f.q.Publish(globalStr, "global.kinu.supervisor.inbox", 1, t0.Add(10*time.Minute))
	f.q.Publish(globalStr, "global.kinu.supervisor.inbox", 2, t0.Add(20*time.Minute))
	f.q.Publish(globalStr, "global.kinu.supervisor.inbox", 3, t0.Add(30*time.Minute))
	f.q.AddDurable(globalStr, mailtest.DurableSpec{
		Name: "mcp_global_builder-0_a", Live: true, Floor: 0, Filters: []string{"global.kinu.supervisor.inbox"},
		LastAck: t0.Add(45 * time.Minute),
	})

	got := read(t, f.reader(globalOf("kinu")))

	if v := need[int64](t, "Depth", got.Depth); v != 5 {
		t.Errorf("Depth = %d, want 5 (2 local + 3 global)", v)
	}
	// local oldest: message 2 stored t0+2m, age 58m. global oldest: stored
	// t0+10m, age 50m. The maximum is the local one.
	if v := need[time.Duration](t, "OldestAge", got.OldestAge); v != 58*time.Minute {
		t.Errorf("OldestAge = %v, want 58m", v)
	}
	if v := need[time.Time](t, "LastAckAt", got.LastAckAt); !v.Equal(t0.Add(45 * time.Minute)) {
		t.Errorf("LastAckAt = %v, want the global tier's later ack", v)
	}
	// Sequences of two streams are not comparable, so the seat does not
	// report one.
	if k := got.AckFloor.Kind(); k != mail.KindUnsupported {
		t.Errorf("AckFloor reads %v across two streams, want unsupported", k)
	}
	if k := got.OldestSeq.Kind(); k != mail.KindUnsupported {
		t.Errorf("OldestSeq reads %v across two streams, want unsupported", k)
	}
}

// One tier that cannot be read makes the whole seat unknown, with the cause,
// never a depth from the tier that answered.
func TestOneUnknownTierMakesTheSeatUnknown(t *testing.T) {
	f := newFixture(t)
	f.publish(inbox, 1, 2)
	f.durable("mcp_builder-0_a", true, 0, inbox, roleInbox)
	// The global stream was never made, as when the credential cannot see it.

	got, err := f.reader(globalOf("kinu")).Read(context.Background(), seat)

	if err == nil {
		t.Fatal("Read returned no error for a tier that could not be read")
	}
	for name, k := range map[string]mail.Kind{
		"Depth": got.Depth.Kind(), "OldestAge": got.OldestAge.Kind(), "LastAckAt": got.LastAckAt.Kind(),
	} {
		if k != mail.KindUnknown {
			t.Errorf("%s reads %v, want unknown", name, k)
		}
	}
	if !strings.Contains(got.Note, "global") {
		t.Errorf("Note = %q, want it to name the global tier", got.Note)
	}
}

// An error from the queue on the local tier is the seat's error too.
func TestAQueueErrorOnTheLocalTierIsAnUnknownSeat(t *testing.T) {
	f := newFixture(t)
	f.publish(inbox, 1)
	f.durable("mcp_builder-0_a", true, 0, inbox, roleInbox)
	boom := errors.New("queue: denied")
	f.q.FailOn(1, boom)

	got, err := f.reader(nil).Read(context.Background(), seat)

	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want the queue's", err)
	}
	if got.Depth.Kind() != mail.KindUnknown {
		t.Errorf("Depth reads %v, want unknown", got.Depth.Kind())
	}
}

// A broker that does not stamp its answer leaves the age on the reader's
// clock, and the note says so (section 11.2).
func TestAnUnstampedAnswerAgesOnTheReadersClockAndSaysSo(t *testing.T) {
	f := newFixture(t)
	f.q.Unstamped = true
	f.publish(inbox, 1)
	f.durable("mcp_builder-0_a", true, 0, inbox, roleInbox)

	got := read(t, f.reader(nil))

	if v := need[time.Duration](t, "OldestAge", got.OldestAge); v != 59*time.Minute {
		t.Errorf("OldestAge = %v, want 59m on the reader's clock", v)
	}
	if !strings.Contains(got.Note, "reader's clock") {
		t.Errorf("Note = %q, want it to say the age is on the reader's clock", got.Note)
	}
}

// Subject tokens come from the seat's own names, so one that could carry a
// wildcard or a separator is refused, not placed in a filter.
func TestASeatWhoseNameCouldBeASubjectWildcardIsRefused(t *testing.T) {
	f := newFixture(t)
	for _, bad := range []api.Session{
		{Name: "a.b", Workspace: "aae", Team: "arcaven", Role: "builder"},
		{Name: "a*", Workspace: "aae", Team: "arcaven", Role: "builder"},
		{Name: "builder-0", Workspace: "a>", Team: "arcaven", Role: "builder"},
		{Name: "", Workspace: "aae", Team: "arcaven", Role: "builder"},
	} {
		got, err := f.reader(nil).Read(context.Background(), bad)
		if err == nil {
			t.Errorf("Read(%+v) returned no error", bad)
		}
		if got.Depth.Kind() != mail.KindUnknown {
			t.Errorf("Read(%+v): Depth reads %v, want unknown", bad, got.Depth.Kind())
		}
	}
}

func TestACancelledContextIsAnUnknownSeat(t *testing.T) {
	f := newFixture(t)
	f.publish(inbox, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := f.reader(nil).Read(ctx, seat)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if got.Depth.Kind() != mail.KindUnknown {
		t.Errorf("Depth reads %v, want unknown", got.Depth.Kind())
	}
}

// The director's global address is the other one the design names.
func TestTheGlobalAddressesAreTheDesignsNames(t *testing.T) {
	if g := mail.SupervisorGlobal("kinu"); g.Stream != "GLOBAL_TO_kinu" || g.Subject != "global.kinu.supervisor.inbox" {
		t.Errorf("SupervisorGlobal(kinu) = %+v", g)
	}
	if g := mail.DirectorGlobal(); g.Stream != "GLOBAL_TO_DIRECTOR" || g.Subject != "global.director.inbox" {
		t.Errorf("DirectorGlobal() = %+v", g)
	}
}

// The age runs on the broker's clock when the broker gives one, whatever the
// reader's own clock says (section 11.2): no host clock enters.
func TestTheAgeRunsOnTheBrokersClockNotTheReaders(t *testing.T) {
	f := newFixture(t)
	f.publish(inbox, 1)
	f.durable("mcp_builder-0_a", true, 0, inbox, roleInbox)
	r := f.reader(nil)
	r.Now = func() time.Time { return t0.Add(5 * time.Hour) } // the reader's clock is far off

	got := read(t, r)

	if v := need[time.Duration](t, "OldestAge", got.OldestAge); v != 59*time.Minute {
		t.Errorf("OldestAge = %v, want 59m on the broker's clock (t0+1h less t0+1m)", v)
	}
	if strings.Contains(got.Note, "reader's clock") {
		t.Errorf("Note = %q: the broker stamped its answer", got.Note)
	}
}

// With several departed durables and no live one, the one that acked furthest
// holds the inherited floor, because that is where a successor resumes.
func TestTheFurthestAckedDepartedDurableSuppliesTheInheritedFloor(t *testing.T) {
	f := newFixture(t)
	f.publish(inbox, 1, 2, 3, 4, 5, 6)
	f.durable("mcp_builder-0_old1", false, 3, inbox, roleInbox)
	f.durable("mcp_builder-0_old2", false, 5, inbox, roleInbox)

	got := read(t, f.reader(nil))

	if v := need[int64](t, "Depth", got.Depth); v != 1 {
		t.Errorf("Depth = %d, want 1 (from the furthest floor 5)", v)
	}
	if v := need[uint64](t, "AckFloor", got.AckFloor); v != 5 {
		t.Errorf("AckFloor = %d, want 5", v)
	}
}
