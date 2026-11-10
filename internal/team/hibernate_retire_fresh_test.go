package team

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// Hibernate and retire end the pane, so a handoff written before the request
// must not end it: the work done after that handoff would be lost for good
// (marvel#1391, the same rule marvel#1388 put on shifts).

// stampMarker writes the seat's handoff and stamps its modification time.
func (f *retireFixture) stampMarker(sess api.Session, role api.Role, at time.Time) {
	f.t.Helper()
	f.writeMarker(sess, role)
	path, _ := api.ExpandHandoffPath(role.Shift.Handoff, sess.Name)
	if err := os.Chtimes(path, at, at); err != nil {
		f.t.Fatal(err)
	}
}

func TestHibernateIgnoresAHandoffWrittenBeforeTheRequest(t *testing.T) {
	role := hibernateRole("worker", realTempDir(t), 1)
	f := newRetireFixture(t, "test-hib-stale", role)
	sess := f.seat("worker", 0)
	f.stampMarker(sess, role, listEpoch.Add(-time.Hour))
	f.hibernate(HibernateOptions{Actor: "op", Source: "cli"})

	for range 3 {
		f.tick()
		f.clock.Advance(time.Second)
	}
	if f.gone(sess) {
		t.Fatal("hibernate ended the seat on a marker older than the request")
	}

	f.stampMarker(sess, role, listEpoch.Add(time.Minute))
	f.clock.Advance(time.Minute)
	f.tick() // reads the new file
	f.clock.Advance(time.Second)
	f.tick()
	if !f.gone(sess) {
		t.Fatal("hibernate did not end the seat after its handoff was written again")
	}
	if got := f.outcomes(); len(got) != 1 || !strings.Contains(got[0], "outcome=handoff-written") {
		t.Fatalf("session.retired = %q, want handoff-written", got)
	}
}

func TestRetireIgnoresAHandoffWrittenBeforeTheRequest(t *testing.T) {
	f := newRetireFixture(t, "test-retire-stale", handoffRole(realTempDir(t)))
	role := f.team().Roles[0]
	sess := f.seat(testShiftRole, 0)
	f.stampMarker(sess, role, listEpoch.Add(-time.Hour))
	f.retire(sess)

	for range 3 {
		f.advance()
		f.clock.Advance(time.Second)
	}
	if f.gone(sess) {
		t.Fatal("retire killed the seat on a marker older than the request")
	}

	f.stampMarker(sess, role, listEpoch.Add(time.Minute))
	f.clock.Advance(time.Minute)
	f.advance() // reads the new file
	f.clock.Advance(time.Second)
	f.advance()
	if !f.gone(sess) {
		t.Fatal("retire did not end the seat after its handoff was written again")
	}
	if got := f.outcomes(); len(got) != 1 || !strings.Contains(got[0], "outcome=handoff-written") {
		t.Fatalf("session.retired = %q, want handoff-written", got)
	}
}

// A request made at a sub-second instant counts a handoff stamped earlier in
// that second, as the shift path does: the comparison is to the second.
func TestHibernateCountsAHandoffStampedInTheRequestsSecond(t *testing.T) {
	role := hibernateRole("worker", realTempDir(t), 1)
	f := newRetireFixture(t, "test-hib-subsecond", role)
	sess := f.seat("worker", 0)
	f.clock.Advance(700 * time.Millisecond)
	f.hibernate(HibernateOptions{Actor: "op", Source: "cli"})
	f.tick()
	f.stampMarker(sess, role, listEpoch.Add(100*time.Millisecond))
	f.clock.Advance(time.Second)
	f.tick()
	f.clock.Advance(time.Second)
	f.tick()
	if !f.gone(sess) {
		t.Fatal("a handoff stamped in the request's second did not end the seat")
	}
}

func TestRetireCountsAHandoffStampedInTheRequestsSecond(t *testing.T) {
	f := newRetireFixture(t, "test-retire-subsecond", handoffRole(realTempDir(t)))
	role := f.team().Roles[0]
	sess := f.seat(testShiftRole, 0)
	f.clock.Advance(700 * time.Millisecond)
	f.retire(sess)
	f.advance()
	f.stampMarker(sess, role, listEpoch.Add(100*time.Millisecond))
	f.clock.Advance(time.Second)
	f.advance()
	f.clock.Advance(time.Second)
	f.advance()
	if !f.gone(sess) {
		t.Fatal("a handoff stamped in the request's second did not end the seat")
	}
}
