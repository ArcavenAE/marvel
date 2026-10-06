package team

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// The view notice (docs/design/readonly-view.md section 5): one pending notice
// per view, coalesced, sent on the max-age handoff timing, with delivery and the
// grace start recorded on the team.

const (
	noticeShaOne   = "1111111111111111111111111111111111111111"
	noticeShaTwo   = "2222222222222222222222222222222222222222"
	noticeShaThree = "3333333333333333333333333333333333333333"
	noticePath     = "/views/ws/squad-" + testShiftRole + "-g1-0/repo/cur"
)

func viewRole() api.Role {
	return api.Role{
		Name: testShiftRole, Replicas: 1,
		Runtime: api.Runtime{Name: "claude", Command: "claude"},
		Views:   []api.View{{Name: "repo", Remote: "remote", Ref: "main"}},
	}
}

// noticeFixture is a seat with a view on a fixed clock, with its context last
// active lastActive ago.
func noticeFixture(t *testing.T, ws string, lastActive time.Duration) (*listFixture, api.Session) {
	t.Helper()
	f := newListFixture(t, ws, viewRole())
	sess := f.seed(time.Hour, lastActive, 0, 0)
	f.quietSince(sess, f.clock.Now().Add(-lastActive))
	return f, sess
}

func (f *listFixture) moved(sess api.Session, commit string) {
	f.t.Helper()
	f.ctrl.NoteViewMoved(sess, "repo", noticePath, commit)
}

func (f *listFixture) deliver() api.Team {
	f.t.Helper()
	team, err := f.store.GetTeam(f.teamKey)
	if err != nil {
		f.t.Fatal(err)
	}
	f.ctrl.deliverViewNotices(&team)
	got, err := f.store.GetTeam(f.teamKey)
	if err != nil {
		f.t.Fatal(err)
	}
	return got
}

func (f *listFixture) quietSince(sess api.Session, at time.Time) {
	f.t.Helper()
	if err := f.store.UpdateSession(sess.Key(), func(live *api.Session) error {
		live.ContextAt = at // UpdateSessionContext stamps wall time, not the fake clock
		return nil
	}); err != nil {
		f.t.Fatal(err)
	}
}

func noticeKey(sess api.Session) string { return sess.Key() + "/repo" }

// A seat busy through three refreshes is told once, about the latest commit,
// when its pane is next quiet.
func TestViewNoticeIsCoalescedAndNamesTheLatestCommit(t *testing.T) {
	f, sess := noticeFixture(t, "test-vn-coalesce", 5*time.Second)

	for _, sha := range []string{noticeShaOne, noticeShaTwo, noticeShaThree} {
		f.moved(sess, sha)
		f.clock.Advance(time.Minute)
		f.quietSince(sess, f.clock.Now().Add(-5*time.Second))
		f.deliver()
	}
	if len(f.notices) != 0 {
		t.Fatalf("a busy seat was sent %d notices: %v", len(f.notices), f.notices)
	}

	f.quietSince(sess, f.clock.Now().Add(-3*time.Minute))
	got := f.deliver()

	if len(f.notices) != 1 {
		t.Fatalf("notices = %d, want exactly one: %v", len(f.notices), f.notices)
	}
	want := "marvel: view repo is now " + noticeShaThree[:12] + ". If your working directory is under " + noticePath +
		", cd to " + noticePath + " again; check with: cat " + noticePath + "/VIEW_SHA"
	if !strings.HasSuffix(f.notices[0], ": "+want) {
		t.Errorf("notice = %q, want the design's text ending %q", f.notices[0], want)
	}
	n := got.ViewNotices[noticeKey(sess)]
	if n.Pending() || n.DeliveredCommit != noticeShaThree || !n.DeliveredAt.Equal(f.clock.Now()) {
		t.Errorf("record = %+v, want delivered of %s now", n, noticeShaThree)
	}
	f.deliver()
	if len(f.notices) != 1 {
		t.Errorf("a delivered notice was sent again: %v", f.notices)
	}
}

// A refused notice stays pending, says why, and is tried again on the next
// tick.
func TestRefusedViewNoticeStaysPendingAndIsRetried(t *testing.T) {
	f, sess := noticeFixture(t, "test-vn-refused", 5*time.Minute)
	refusals := 2
	f.ctrl.Notify = func(s api.Session, text string) error {
		if refusals > 0 {
			refusals--
			return errors.New("refused: update menu")
		}
		f.notices = append(f.notices, text)
		return nil
	}
	f.moved(sess, noticeShaOne)

	for i := 0; i < 2; i++ {
		got := f.deliver()
		n := got.ViewNotices[noticeKey(sess)]
		if !n.Pending() || !strings.Contains(n.Undelivered, "update menu") {
			t.Fatalf("after refusal %d: %+v, want pending with the cause", i+1, n)
		}
		f.clock.Advance(30 * time.Second)
		f.quietSince(sess, f.clock.Now().Add(-5*time.Minute))
	}
	got := f.deliver()

	if len(f.notices) != 1 {
		t.Fatalf("notices = %d after the third try, want 1", len(f.notices))
	}
	if n := got.ViewNotices[noticeKey(sess)]; n.Pending() || n.Undelivered != "" || n.DeliveredCommit != noticeShaOne {
		t.Errorf("record = %+v, want delivered with the refusal cleared", n)
	}
}

// At max_defer the notice goes however busy the seat, and the grace does not
// start until the pane is next quiet.
func TestViewNoticeAtMaxDeferMidTurnStartsNoGraceUntilQuiet(t *testing.T) {
	f, sess := noticeFixture(t, "test-vn-defer", 5*time.Second)
	f.moved(sess, noticeShaOne)

	f.clock.Advance(api.DefaultShiftMaxDefer - time.Second)
	f.quietSince(sess, f.clock.Now().Add(-5*time.Second))
	f.deliver()
	if len(f.notices) != 0 {
		t.Fatalf("sent before max_defer: %v", f.notices)
	}

	f.clock.Advance(time.Second)
	f.quietSince(sess, f.clock.Now().Add(-5*time.Second))
	got := f.deliver()
	if len(f.notices) != 1 {
		t.Fatalf("notices = %d at max_defer, want 1", len(f.notices))
	}
	if n := got.ViewNotices[noticeKey(sess)]; !n.GraceStart.IsZero() {
		t.Fatalf("grace started at a mid-turn delivery: %+v", n)
	}

	f.clock.Advance(time.Minute)
	f.quietSince(sess, f.clock.Now().Add(-5*time.Second))
	if n := f.deliver().ViewNotices[noticeKey(sess)]; !n.GraceStart.IsZero() {
		t.Fatalf("grace started while the seat was still busy: %+v", n)
	}

	f.clock.Advance(4 * time.Minute)
	f.quietSince(sess, f.clock.Now().Add(-3*time.Minute))
	n := f.deliver().ViewNotices[noticeKey(sess)]
	if !n.GraceStart.Equal(f.clock.Now()) {
		t.Errorf("GraceStart = %v, want the first quiet observation %v", n.GraceStart, f.clock.Now())
	}
	f.clock.Advance(time.Minute)
	if again := f.deliver().ViewNotices[noticeKey(sess)]; !again.GraceStart.Equal(n.GraceStart) {
		t.Errorf("GraceStart moved from %v to %v on a later tick", n.GraceStart, again.GraceStart)
	}
}

// A notice delivered to a seat that was already quiet starts its grace on a
// later tick, not in the tick that sent it.
func TestViewNoticeGraceStartsAfterTheDeliveringTick(t *testing.T) {
	f, sess := noticeFixture(t, "test-vn-quiet", 10*time.Minute)
	f.moved(sess, noticeShaOne)

	got := f.deliver()
	if n := got.ViewNotices[noticeKey(sess)]; n.DeliveredAt.IsZero() || !n.GraceStart.IsZero() {
		t.Fatalf("record = %+v, want delivered with no grace yet", n)
	}
	f.clock.Advance(30 * time.Second)
	n := f.deliver().ViewNotices[noticeKey(sess)]
	if !n.GraceStart.Equal(f.clock.Now()) {
		t.Errorf("GraceStart = %v, want %v on the next tick", n.GraceStart, f.clock.Now())
	}
}

// A move after a delivered notice starts a new pending notice and clears the
// record for the old commit.
func TestViewMoveAfterDeliveryStartsANewNotice(t *testing.T) {
	f, sess := noticeFixture(t, "test-vn-again", 10*time.Minute)
	f.moved(sess, noticeShaOne)
	f.deliver()
	f.clock.Advance(time.Minute)
	f.deliver()

	f.clock.Advance(time.Minute)
	f.moved(sess, noticeShaTwo)
	n := f.store2(t).ViewNotices[noticeKey(sess)]
	if !n.Pending() || n.Commit != noticeShaTwo || !n.GraceStart.IsZero() || !n.PendingSince.Equal(f.clock.Now()) {
		t.Fatalf("record = %+v, want a pending notice for %s with no grace", n, noticeShaTwo)
	}
	f.deliver()
	if len(f.notices) != 2 {
		t.Errorf("notices = %d, want one per distinct move", len(f.notices))
	}
}

func (f *listFixture) store2(t *testing.T) api.Team {
	t.Helper()
	team, err := f.store.GetTeam(f.teamKey)
	if err != nil {
		t.Fatal(err)
	}
	return team
}

// A seat that is gone has no notice to deliver, and its record is dropped.
func TestViewNoticeIsDroppedWithItsSeat(t *testing.T) {
	f, sess := noticeFixture(t, "test-vn-gone", 10*time.Minute)
	f.moved(sess, noticeShaOne)
	if err := f.store.UpdateSession(sess.Key(), func(s *api.Session) error {
		s.State = api.SessionCrashed
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	got := f.deliver()

	if len(f.notices) != 0 {
		t.Errorf("a notice was sent to a seat that is gone: %v", f.notices)
	}
	if _, ok := got.ViewNotices[noticeKey(sess)]; ok {
		t.Errorf("the record outlived its seat: %+v", got.ViewNotices)
	}
}
