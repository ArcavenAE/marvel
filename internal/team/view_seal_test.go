package team

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// Sealing a superseded tree (docs/design/readonly-view.md section 6): only the
// notice moves a tree, and only after delivery, the next quiet and the grace.

const (
	treeA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	treeB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	treeC = "cccccccccccccccccccccccccccccccccccccccc"
	treeD = "dddddddddddddddddddddddddddddddddddddddd"
)

// sealFixture is a notice fixture whose view declares grace, with a recorder
// for the seals the controller asks for.
type sealFixture struct {
	*listFixture
	sess    api.Session
	seals   [][]string
	sealErr error
}

func newSealFixture(t *testing.T, ws string, lastActive, grace time.Duration) *sealFixture {
	t.Helper()
	role := viewRole()
	role.Views[0].ReenterGrace = grace
	f := newListFixture(t, ws, role)
	sess := f.seed(time.Hour, lastActive, 0, 0)
	f.quietSince(sess, f.clock.Now().Add(-lastActive))
	sf := &sealFixture{listFixture: f, sess: sess}
	f.ctrl.SealViewTrees = func(s api.Session, view string, commits []string) error {
		if s.Key() != sess.Key() || view != "repo" {
			t.Errorf("seal asked for %s/%s", s.Key(), view)
		}
		if sf.sealErr != nil {
			return sf.sealErr
		}
		sf.seals = append(sf.seals, slices.Clone(commits))
		return nil
	}
	return sf
}

func (f *sealFixture) move(prev, commit string) {
	f.t.Helper()
	f.ctrl.NoteViewMoved(f.sess, "repo", noticePath, prev, commit)
}

func (f *sealFixture) record() api.ViewNotice {
	f.t.Helper()
	team, err := f.store.GetTeam(f.teamKey)
	if err != nil {
		f.t.Fatal(err)
	}
	return team.ViewNotices[noticeKey(f.sess)]
}

// settle makes the pane quiet as of now and runs a tick.
func (f *sealFixture) settle() {
	f.t.Helper()
	f.quietSince(f.sess, f.clock.Now().Add(-10*time.Minute))
	f.deliver()
}

func (f *sealFixture) busy() {
	f.t.Helper()
	f.quietSince(f.sess, f.clock.Now().Add(-5*time.Second))
}

// Each move lists the tree it left, oldest first, once; a view that returns to
// a tree makes it current again, so it is no longer superseded.
func TestNoteViewMovedListsTheSupersededTrees(t *testing.T) {
	f := newSealFixture(t, "test-seal-list", 10*time.Minute, 2*time.Minute)
	f.move(treeA, treeB)
	f.move(treeB, treeC)
	f.move(treeB, treeC) // a repeated report of the same move
	if got := f.record().Superseded; !slices.Equal(got, []string{treeA, treeB}) {
		t.Fatalf("superseded = %v, want [A B]", got)
	}
	f.move(treeC, treeA)
	if got := f.record().Superseded; !slices.Equal(got, []string{treeB, treeC}) {
		t.Fatalf("superseded after returning to A = %v, want [B C]", got)
	}
}

func TestViewTreesHeldCountsTheReadableTrees(t *testing.T) {
	f := newSealFixture(t, "test-seal-held", 10*time.Minute, 2*time.Minute)
	if n := f.ctrl.ViewTreesHeld(f.sess, "repo"); n != 0 {
		t.Fatalf("held with no record = %d", n)
	}
	f.move(treeA, treeB)
	f.move(treeB, treeC)
	if n := f.ctrl.ViewTreesHeld(f.sess, "repo"); n != 2 {
		t.Errorf("held = %d, want 2", n)
	}
	if n := f.ctrl.ViewTreesHeld(f.sess, "other"); n != 0 {
		t.Errorf("held for another view = %d, want 0", n)
	}
}

// However long the notice stays undelivered, and however many refreshes happen,
// no tree is sealed.
func TestNoTreeIsSealedWhileTheNoticeIsUndelivered(t *testing.T) {
	f := newSealFixture(t, "test-seal-undelivered", 10*time.Minute, 0)
	f.ctrl.Notify = func(api.Session, string, string) error { return errors.New("refused: update menu") }
	f.move(treeA, treeB)
	f.move(treeB, treeC)
	for i := 0; i < 8; i++ {
		f.clock.Advance(30 * time.Minute)
		f.settle()
	}
	if len(f.seals) != 0 {
		t.Fatalf("trees were sealed before their seat was told: %v", f.seals)
	}
	if got := f.record().Superseded; !slices.Equal(got, []string{treeA, treeB}) {
		t.Errorf("superseded = %v, want both still readable", got)
	}
}

// Delivery, then the next observed quiet, then the grace: trees older than the
// commit the notice named are sealed, and the record says so.
func TestTreesAreSealedAfterDeliveryQuietAndGrace(t *testing.T) {
	f := newSealFixture(t, "test-seal-after", 10*time.Minute, 2*time.Minute)
	f.move(treeA, treeB)
	f.move(treeB, treeC)

	f.settle() // delivers the notice
	if len(f.notices) != 1 || len(f.seals) != 0 {
		t.Fatalf("delivery tick: notices %d, seals %v; want one notice and no seal", len(f.notices), f.seals)
	}
	f.clock.Advance(30 * time.Second)
	f.settle() // the first quiet after delivery starts the grace
	f.clock.Advance(time.Minute)
	f.settle()
	if len(f.seals) != 0 {
		t.Fatalf("sealed %v inside the grace", f.seals)
	}
	f.clock.Advance(time.Minute)
	f.settle()
	if len(f.seals) != 1 || !slices.Equal(f.seals[0], []string{treeA, treeB}) {
		t.Fatalf("seals = %v, want one call sealing [A B]", f.seals)
	}
	if n := f.record(); len(n.Superseded) != 0 || !slices.Equal(n.Sealed, []string{treeA, treeB}) {
		t.Errorf("record = superseded %v sealed %v, want none and [A B]", n.Superseded, n.Sealed)
	}
	f.clock.Advance(time.Hour)
	f.settle()
	if len(f.seals) != 1 {
		t.Errorf("a sealed tree was sealed again: %v", f.seals)
	}
}

// A zero grace seals at the first quiet after delivery, on that tick.
func TestZeroGraceSealsAtTheQuiet(t *testing.T) {
	f := newSealFixture(t, "test-seal-zero", 10*time.Minute, 0)
	f.move(treeA, treeB)
	f.settle()
	if len(f.seals) != 0 {
		t.Fatalf("sealed on the delivery tick: %v", f.seals)
	}
	f.clock.Advance(30 * time.Second)
	f.settle()
	if len(f.seals) != 1 || !slices.Equal(f.seals[0], []string{treeA}) {
		t.Fatalf("seals = %v, want [A] at the quiet", f.seals)
	}
}

// A notice forced out mid-turn at max_defer starts no grace, and so seals
// nothing, until the pane is next quiet.
func TestNoSealUntilTheQuietAfterAMidTurnDelivery(t *testing.T) {
	f := newSealFixture(t, "test-seal-midturn", 5*time.Second, 0)
	f.move(treeA, treeB)
	f.clock.Advance(api.DefaultShiftMaxDefer)
	f.busy()
	f.deliver()
	if len(f.notices) != 1 {
		t.Fatalf("notices = %d, want the forced one", len(f.notices))
	}
	f.clock.Advance(time.Hour)
	f.busy()
	f.deliver()
	if len(f.seals) != 0 {
		t.Fatalf("sealed %v with the pane never quiet", f.seals)
	}
	f.settle()
	if len(f.seals) != 1 {
		t.Fatalf("seals = %v, want one at the quiet", f.seals)
	}
}

// The tree the seat was told about stays readable until a later notice
// delivers: only trees older than the notice's commit are sealed.
func TestTheTreeTheSeatWasToldAboutIsNotSealed(t *testing.T) {
	f := newSealFixture(t, "test-seal-told", 10*time.Minute, 0)
	f.move(treeA, treeB)
	f.settle()
	f.clock.Advance(30 * time.Second)
	f.settle()
	if len(f.seals) != 1 || !slices.Equal(f.seals[0], []string{treeA}) {
		t.Fatalf("first seal = %v, want [A]", f.seals)
	}

	f.move(treeB, treeC)
	f.clock.Advance(30 * time.Second)
	f.settle()
	if n := f.record(); !slices.Equal(n.Superseded, []string{treeB}) || !slices.Equal(n.Sealed, []string{treeA}) {
		t.Fatalf("record = superseded %v sealed %v, want B readable and A sealed", n.Superseded, n.Sealed)
	}
	if len(f.seals) != 1 {
		t.Fatalf("B was sealed before the seat was told about C: %v", f.seals)
	}

	f.clock.Advance(30 * time.Second)
	f.settle()
	f.clock.Advance(30 * time.Second)
	f.settle()
	if len(f.seals) != 2 || !slices.Equal(f.seals[1], []string{treeB}) {
		t.Errorf("seals = %v, want B sealed after C was delivered", f.seals)
	}
}

// A seal that fails leaves the trees readable and is tried again.
func TestAFailedSealLeavesTheTreesReadableAndIsRetried(t *testing.T) {
	f := newSealFixture(t, "test-seal-retry", 10*time.Minute, 0)
	f.move(treeA, treeB)
	f.settle()
	f.sealErr = errors.New("disk on fire")
	f.clock.Advance(30 * time.Second)
	f.settle()
	f.clock.Advance(30 * time.Second)
	f.settle()
	if n := f.record(); !slices.Equal(n.Superseded, []string{treeA}) || len(n.Sealed) != 0 {
		t.Fatalf("record after a failed seal = %+v, want A still readable", n)
	}
	f.sealErr = nil
	f.clock.Advance(30 * time.Second)
	f.settle()
	if len(f.seals) != 1 {
		t.Fatalf("seals = %v, want the retry to land once", f.seals)
	}
	if n := f.record(); len(n.Superseded) != 0 || !slices.Equal(n.Sealed, []string{treeA}) {
		t.Errorf("record = %+v, want A sealed", n)
	}
}

// With no sealer installed the trees stay readable; nothing fails.
func TestWithNoSealerTheTreesStayReadable(t *testing.T) {
	f := newSealFixture(t, "test-seal-none", 10*time.Minute, 0)
	f.ctrl.SealViewTrees = nil
	f.move(treeA, treeB)
	f.settle()
	f.clock.Advance(time.Minute)
	f.settle()
	if got := f.record().Superseded; !slices.Equal(got, []string{treeA}) {
		t.Errorf("superseded = %v, want A readable", got)
	}
}

// A view that goes away from the commit its seat was told about and comes back
// to it owes no new notice, but the delivery record was cleared by the first
// move. The seat was told about this commit, so the return counts as told and
// the grace runs from the next quiet; otherwise the superseded trees would stay
// readable until some later move, and a held view cannot move.
func TestReturningToTheToldCommitStillSealsTheOlderTrees(t *testing.T) {
	f := newSealFixture(t, "test-seal-return", 10*time.Minute, 0)
	f.move(treeA, treeB)
	f.settle() // the seat is told about B
	f.move(treeB, treeC)
	f.move(treeC, treeB)
	if n := f.record(); n.Pending() {
		t.Fatalf("returning to the told commit left a notice pending: %+v", n)
	}
	f.clock.Advance(30 * time.Second)
	f.settle()
	f.clock.Advance(30 * time.Second)
	f.settle()
	if len(f.notices) != 1 {
		t.Errorf("notices = %d, want only the first", len(f.notices))
	}
	if n := f.record(); len(n.Superseded) != 0 || len(n.Sealed) != 2 {
		t.Errorf("record = superseded %v sealed %v, want A and C sealed", n.Superseded, n.Sealed)
	}
}
