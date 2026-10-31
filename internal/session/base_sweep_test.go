package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// baseRoot is a scratch directory standing in for /tmp.
func baseRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// mkTempBase makes a directory under root with exactly the given mode.
func mkTempBase(t *testing.T, root, name string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func sweeperForBases(root string) baseSweeper {
	return baseSweeper{root: root, owned: ownedByMe}
}

func baseEntry(t *testing.T, sw EmptyBaseSweep, path string) EmptyBaseEntry {
	t.Helper()
	for _, b := range sw.Bases {
		if b.Path == path {
			return b
		}
	}
	t.Fatalf("base %s is not in the sweep: %+v", path, sw.Bases)
	return EmptyBaseEntry{}
}

func TestEmptyBaseSweepRemovesOnlyAnEmptyPrivateOwnedBase(t *testing.T) {
	root := baseRoot(t)
	empty1 := mkTempBase(t, root, "marvel-h-aaaaaaaa", 0o700)
	empty2 := mkTempBase(t, root, "marvel-h-01234567", 0o700)

	withHome := mkTempBase(t, root, "marvel-h-11111111", 0o700)
	mkHome(t, withHome, "bbbbbbbbbbbb", true)
	withFile := mkTempBase(t, root, "marvel-h-12121212", 0o700)
	if err := os.WriteFile(filepath.Join(withFile, "stray"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	withEmptyDir := mkTempBase(t, root, "marvel-h-13131313", 0o700) // one level down is not empty
	if err := os.Mkdir(filepath.Join(withEmptyDir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}

	loose := mkTempBase(t, root, "marvel-h-22222222", 0o755)
	wide := mkTempBase(t, root, "marvel-h-23232323", 0o777)
	narrow := mkTempBase(t, root, "marvel-h-24242424", 0o500)
	t.Cleanup(func() { _ = os.Chmod(narrow, 0o700) })

	target := mkTempBase(t, root, "target", 0o700)
	link := filepath.Join(root, "marvel-h-33333333")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	notDir := filepath.Join(root, "marvel-h-44444444")
	if err := os.WriteFile(notDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Names that are not marvel-h- and eight lowercase hex digits are never listed.
	var wrongNames []string
	for _, n := range []string{"marvel-h-zz", "marvel-h-1234567", "marvel-h-123456789", "marvel-h-ABCDEF12", "marvel-g-aaaaaaaa", "marvel-h-aaaaaaaa.d"} {
		wrongNames = append(wrongNames, mkTempBase(t, root, n, 0o700))
	}

	sw := sweeperForBases(root).sweep(true)

	for _, gone := range []string{empty1, empty2} {
		if exists(gone) || !baseEntry(t, sw, gone).Removed {
			t.Errorf("%s: an empty private base was not removed", gone)
		}
	}
	for _, kept := range []string{withHome, withFile, withEmptyDir, loose, wide, narrow, link, notDir, target} {
		if !exists(kept) {
			t.Errorf("%s was removed", kept)
		}
	}
	for _, listed := range []string{withHome, withFile, withEmptyDir, loose, wide, narrow, link, notDir} {
		if e := baseEntry(t, sw, listed); e.State != BaseKept || e.Removed || e.Why == "" {
			t.Errorf("%s: entry %+v, want kept with a reason", listed, e)
		}
	}
	for _, p := range wrongNames {
		if !exists(p) {
			t.Errorf("%s was removed although its name is not a layout base", p)
		}
		for _, b := range sw.Bases {
			if b.Path == p {
				t.Errorf("%s was listed", p)
			}
		}
	}
	if sw.Empty != 2 || sw.Removed != 2 {
		t.Errorf("empty=%d removed=%d, want 2 and 2", sw.Empty, sw.Removed)
	}
	if !exists(filepath.Join(withHome, "bbbbbbbbbbbb", "sessions", "rollout.jsonl")) {
		t.Error("a home inside a kept base was touched")
	}
}

func TestEmptyBaseSweepDryRunChangesNothing(t *testing.T) {
	root := baseRoot(t)
	mkTempBase(t, root, "marvel-h-aaaaaaaa", 0o700)
	mkTempBase(t, root, "marvel-h-bbbbbbbb", 0o700)
	full := mkTempBase(t, root, "marvel-h-cccccccc", 0o700)
	mkHome(t, full, "dddddddddddd", false)

	before := snapshot(t, root)
	sw := sweeperForBases(root).sweep(false)
	if sw.Confirmed || sw.Removed != 0 {
		t.Errorf("a dry run reports %+v", sw)
	}
	if sw.Empty != 2 {
		t.Errorf("dry run lists %d empty bases, want 2", sw.Empty)
	}
	if after := snapshot(t, root); after != before {
		t.Errorf("a dry run changed the tree:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestEmptyBaseSweepKeepsABaseThatAnotherUserOwns(t *testing.T) {
	// A directory owned by another user cannot be made without privilege, so the
	// ownership answer is the seam: the check must consult it and keep the base.
	root := baseRoot(t)
	other := mkTempBase(t, root, "marvel-h-aaaaaaaa", 0o700)
	s := sweeperForBases(root)
	s.owned = func(os.FileInfo) bool { return false }
	sw := s.sweep(true)
	if !exists(other) {
		t.Error("a base the ownership check refused was removed")
	}
	if e := baseEntry(t, sw, other); e.Removed || !strings.Contains(e.Why, "not owned") {
		t.Errorf("entry = %+v", e)
	}
}

func TestEmptyBaseSweepNeverListsThisLayoutsOwnBase(t *testing.T) {
	root := baseRoot(t)
	own := mkTempBase(t, root, "marvel-h-aaaaaaaa", 0o700)
	s := sweeperForBases(root)
	s.ownBase = "marvel-h-aaaaaaaa"
	sw := s.sweep(true)
	if !exists(own) || len(sw.Bases) != 0 {
		t.Errorf("this layout's own empty base was listed or removed: %+v", sw)
	}
}

func TestEmptyBaseSweepIsOneRmdirNotARecursiveDelete(t *testing.T) {
	// A base that gains an entry between the emptiness check and the removal is
	// kept by the kernel: rmdir refuses a directory that is not empty.
	root := baseRoot(t)
	base := mkTempBase(t, root, "marvel-h-aaaaaaaa", 0o700)
	s := sweeperForBases(root)
	s.beforeRemove = func(path string) {
		if err := os.WriteFile(filepath.Join(path, "late"), []byte("x"), 0o600); err != nil {
			t.Error(err)
		}
	}
	sw := s.sweep(true)
	if !exists(filepath.Join(base, "late")) {
		t.Error("an entry that appeared before the removal was deleted")
	}
	if e := baseEntry(t, sw, base); e.Removed || e.State != BaseKept || !strings.Contains(e.Why, "not removed") {
		t.Errorf("entry = %+v, want kept with the rmdir refusal", e)
	}
	if sw.Removed != 0 {
		t.Errorf("removed = %d, want 0", sw.Removed)
	}
}
