package team

import (
	"errors"
	"os"
	"syscall"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// docs/design/read-only-seat-handoff.md, section 2: the directory marvel
// records when it asks for a handoff is held open until the request ends, and a
// put compares the directory it opens with the held one (os.SameFile). A device
// and inode number read earlier cannot promise that: a directory freed and
// replaced can take the same number. The numbers stay as the fallback for a
// request restored after a restart, which has nothing held.

func dirNumbers(t *testing.T, dir string) (dev, ino uint64) {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Stat(dir, &st); err != nil {
		t.Fatal(err)
	}
	return uint64(st.Dev), uint64(st.Ino) //nolint:unconvert // Dev is int32 on darwin
}

// A directory swapped for another whose numbers equal the recorded ones, as
// inode reuse produces, is refused. The numeric check accepts it; the held
// descriptor does not.
func TestPutRefusesADirectoryReplacedByOneWithTheRecordedNumbers(t *testing.T) {
	f := newPutFixture(t, true)
	old := f.dir + ".old"
	if err := os.Rename(f.dir, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	dev, ino := dirNumbers(t, f.dir)
	if err := f.store.UpdateTeam(f.team, func(live *api.Team) error {
		r := live.ShiftRequests[testShiftRole]
		r.DirDev, r.DirIno = dev, ino
		live.ShiftRequests[testShiftRole] = r
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := f.ctrl.PutHandoff(f.token, "the handoff"); !errors.Is(err, ErrHandoffDir) {
		t.Fatalf("put into a replaced directory with the recorded numbers: err = %v, want ErrHandoffDir", err)
	}
	if got := entries(t, f.dir); len(got) != 0 {
		t.Errorf("a refused put left %v in the replacement directory", got)
	}
}

// The directory the request held is the one the put writes into.
func TestPutWritesIntoTheDirectoryItHeld(t *testing.T) {
	f := newPutFixture(t, true)
	if _, err := f.ctrl.PutHandoff(f.token, "the handoff"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, err := os.Stat(f.path()); err != nil {
		t.Errorf("the handoff file is not at %s: %v", f.path(), err)
	}
}

// A request restored after a restart holds nothing, and falls back to the
// recorded numbers: the real directory is accepted, a different one refused.
func TestPutFallsBackToTheRecordedNumbersWithNothingHeld(t *testing.T) {
	f := newPutFixture(t, true)
	f.ctrl.mu.Lock()
	f.ctrl.closeHandoffDirsLocked()
	f.ctrl.handoffDirs = handoffDirs{}
	f.ctrl.mu.Unlock()

	if _, err := f.ctrl.PutHandoff(f.token, "the handoff"); err != nil {
		t.Fatalf("put into the recorded directory with nothing held: %v", err)
	}

	g := newPutFixture(t, true)
	g.ctrl.mu.Lock()
	g.ctrl.closeHandoffDirsLocked()
	g.ctrl.handoffDirs = handoffDirs{}
	g.ctrl.mu.Unlock()
	if err := g.store.UpdateTeam(g.team, func(live *api.Team) error {
		r := live.ShiftRequests[testShiftRole]
		r.DirIno++
		live.ShiftRequests[testShiftRole] = r
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.ctrl.PutHandoff(g.token, "the handoff"); !errors.Is(err, ErrHandoffDir) {
		t.Fatalf("put with nothing held and a mismatched inode: err = %v, want ErrHandoffDir", err)
	}
}

// One directory is held for a pending request, and it is closed when the
// request ends, so descriptors do not accumulate over many shifts.
func TestRequestHoldsOneDirectoryUntilItEnds(t *testing.T) {
	f := newPutFixture(t, true)
	if n := f.ctrl.heldHandoffDirCount(); n != 1 {
		t.Fatalf("held directories after a request = %d, want 1", n)
	}
	f.dropRequest()
	f.ctrl.mu.Lock()
	f.ctrl.releaseHandoffDirsLocked()
	f.ctrl.mu.Unlock()
	if n := f.ctrl.heldHandoffDirCount(); n != 0 {
		t.Errorf("held directories after the request ended = %d, want 0", n)
	}
}

// heldFile is the descriptor held for the fixture's role, nil when none is.
func (f *putFixture) heldFile() *os.File {
	f.ctrl.mu.Lock()
	defer f.ctrl.mu.Unlock()
	return f.ctrl.handoffDirs.held[handoffDirKey{f.team, testShiftRole}].f
}

func isClosed(f *os.File) bool {
	_, err := f.Stat()
	return errors.Is(err, os.ErrClosed)
}

// A descriptor is closed when its request ends, when a new request for the
// role replaces it, and when the controller stops, so none outlives its use.
func TestHeldDirectoryDescriptorsAreClosed(t *testing.T) {
	f := newPutFixture(t, true)
	first := f.heldFile()
	if first == nil || isClosed(first) {
		t.Fatalf("no open descriptor is held after a request: %v", first)
	}

	f.request()
	second := f.heldFile()
	if second == nil || second == first {
		t.Fatalf("a second request kept the first descriptor: %v", second)
	}
	if !isClosed(first) {
		t.Error("the descriptor of a replaced request is still open")
	}
	if isClosed(second) {
		t.Error("the new request's descriptor is closed")
	}

	f.dropRequest()
	f.ctrl.mu.Lock()
	f.ctrl.releaseHandoffDirsLocked()
	f.ctrl.mu.Unlock()
	if !isClosed(second) {
		t.Error("the descriptor of an ended request is still open")
	}

	f.request()
	third := f.heldFile()
	f.ctrl.mu.Lock()
	f.ctrl.closeHandoffDirsLocked()
	f.ctrl.mu.Unlock()
	if third == nil || !isClosed(third) {
		t.Error("a stopped controller still holds an open descriptor")
	}
	if n := f.ctrl.heldHandoffDirCount(); n != 0 {
		t.Errorf("held directories after the controller stopped = %d, want 0", n)
	}
}
