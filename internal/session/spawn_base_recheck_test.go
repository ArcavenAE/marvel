package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The spawn-time check of the base a harness home goes in. The base is checked
// once at start; a sweep, a temp cleaner or another user can remove it later,
// and in a sticky /tmp any local user can then create a directory at that name.
// Every directory below is under t.TempDir; nothing here reads or writes /tmp.

type recheckBases struct {
	durable, legacy, fallback string
}

// recheckManager is a Manager whose start-time chain is three private bases
// under a scratch root, with the spawn-time check on.
func recheckManager(t *testing.T) (*Manager, recheckBases) {
	t.Helper()
	mgr := homeManager(t)
	root := t.TempDir()
	b := recheckBases{
		durable:  filepath.Join(root, "durable"),
		legacy:   filepath.Join(root, "legacy"),
		fallback: filepath.Join(root, "fallback"),
	}
	for _, p := range []string{b.durable, b.legacy, b.fallback} {
		mkPrivate(t, p)
	}
	mgr.HarnessHomeDir = b.durable
	mgr.harnessChain = []string{b.durable, b.legacy, b.fallback}
	mgr.verifyBases = true
	return mgr, b
}

func mkPrivate(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

// plant puts a directory at path with a mode wider than 0700, as another local
// user could in a sticky directory.
func plant(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o777); err != nil {
		t.Fatal(err)
	}
}

func underBase(home, base string) bool {
	return strings.HasPrefix(home, base+string(filepath.Separator))
}

func entries(t *testing.T, dir string) int {
	t.Helper()
	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(list)
}

func TestSpawnUsesTheBaseWhileItStaysPrivate(t *testing.T) {
	t.Parallel()
	mgr, b := recheckManager(t)
	sess := sessionFor("coder", "codex")
	if plan := mgr.planLaunch(sess); plan.homeRefusal != "" {
		t.Fatalf("refused a good base: %s", plan.homeRefusal)
	}
	if !underBase(sess.HarnessHome, b.durable) {
		t.Errorf("home = %q, want it under %q", sess.HarnessHome, b.durable)
	}
}

// The probe sequence: the base is valid at start, is removed, and a 0777
// directory is planted at its name. The spawn must not make a home in it.
func TestSpawnAfterTheBaseIsReplantedWideUsesTheFallbackAndLeavesThePlantAlone(t *testing.T) {
	t.Parallel()
	mgr, b := recheckManager(t)
	if err := os.Remove(b.durable); err != nil {
		t.Fatal(err)
	}
	plant(t, b.durable)
	sess := sessionFor("coder", "codex")
	plan := mgr.planLaunch(sess)
	if plan.homeRefusal != "" {
		t.Fatalf("refused although a later base was good: %s", plan.homeRefusal)
	}
	if !underBase(sess.HarnessHome, b.legacy) {
		t.Errorf("home = %q, want it under the next base %q", sess.HarnessHome, b.legacy)
	}
	if n := entries(t, b.durable); n != 0 {
		t.Errorf("the planted directory holds %d entries, want 0", n)
	}
	if plan.env["CODEX_HOME"] != sess.HarnessHome {
		t.Errorf("CODEX_HOME = %q, want the recorded home %q", plan.env["CODEX_HOME"], sess.HarnessHome)
	}
}

func TestSpawnRecreatesAnAbsentBaseAt0700(t *testing.T) {
	t.Parallel()
	mgr, b := recheckManager(t)
	if err := os.Remove(b.durable); err != nil {
		t.Fatal(err)
	}
	sess := sessionFor("coder", "codex")
	if plan := mgr.planLaunch(sess); plan.homeRefusal != "" {
		t.Fatalf("refused: %s", plan.homeRefusal)
	}
	if !underBase(sess.HarnessHome, b.durable) {
		t.Errorf("home = %q, want it back under %q", sess.HarnessHome, b.durable)
	}
	fi, err := os.Lstat(b.durable)
	if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Errorf("recreated base = %v, %v; want a 0700 directory", fi, err)
	}
}

func TestSpawnRefusesASymlinkedBase(t *testing.T) {
	t.Parallel()
	mgr, b := recheckManager(t)
	// The target is itself private to this user, so only the link can fail it.
	target := t.TempDir()
	if err := os.Chmod(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(b.durable); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, b.durable); err != nil {
		t.Fatal(err)
	}
	sess := sessionFor("coder", "codex")
	mgr.planLaunch(sess)
	if !underBase(sess.HarnessHome, b.legacy) {
		t.Errorf("home = %q, want it under %q, not through the link", sess.HarnessHome, b.legacy)
	}
	if n := entries(t, target); n != 0 {
		t.Errorf("the link target holds %d entries, want 0", n)
	}
}

func TestSpawnSkipsABaseAnotherUserOwns(t *testing.T) {
	t.Parallel()
	mgr, b := recheckManager(t)
	// A directory owned by another user cannot be made without privilege, so the
	// ownership answer is the seam.
	mgr.ownerCheck = func(fi os.FileInfo) bool { return fi.Name() != "durable" }
	sess := sessionFor("coder", "codex")
	mgr.planLaunch(sess)
	if !underBase(sess.HarnessHome, b.legacy) {
		t.Errorf("home = %q, want it under %q", sess.HarnessHome, b.legacy)
	}
	if n := entries(t, b.durable); n != 0 {
		t.Errorf("the foreign base holds %d entries, want 0", n)
	}
}

func TestSpawnDoesNotKeepARecordedHomeUnderAFailedBase(t *testing.T) {
	t.Parallel()
	mgr, b := recheckManager(t)
	sess := sessionFor("coder", "codex")
	mgr.planLaunch(sess)
	leaf := filepath.Base(sess.HarnessHome)
	// The base is replaced by a wide one that already holds a private home of
	// the same name, which is exactly what a planted directory could carry.
	if err := os.RemoveAll(b.durable); err != nil {
		t.Fatal(err)
	}
	plant(t, b.durable)
	planted := filepath.Join(b.durable, leaf)
	mkPrivate(t, planted)

	again := sessionFor("coder", "codex")
	again.HarnessHome = planted
	mgr.planLaunch(again)
	if underBase(again.HarnessHome, b.durable) {
		t.Errorf("the recorded home under a failed base was kept: %q", again.HarnessHome)
	}
	if !underBase(again.HarnessHome, b.legacy) {
		t.Errorf("home = %q, want it under %q", again.HarnessHome, b.legacy)
	}
}

// A validation failure everywhere refuses the spawn. It does not fall to the
// operator's own home.
func TestSpawnRefusesWhenEveryBaseFailsValidation(t *testing.T) {
	t.Parallel()
	mgr, _ := recheckManager(t)
	mgr.ownerCheck = func(os.FileInfo) bool { return false }
	sess := sessionFor("coder", "codex")
	plan := mgr.planLaunch(sess)
	if plan.homeRefusal == "" {
		t.Fatal("every base was foreign and the spawn was not refused")
	}
	if sess.HarnessHome != "" || plan.env["CODEX_HOME"] != "" {
		t.Errorf("a refused spawn still got a home: %q, CODEX_HOME=%q", sess.HarnessHome, plan.env["CODEX_HOME"])
	}
	if !strings.Contains(plan.homeRefusal, "not owned by this user") {
		t.Errorf("refusal = %q, want it to name why", plan.homeRefusal)
	}
}

// An ordinary error (here a parent that is a file, so Mkdir gives ENOTDIR) is
// not a validation failure, and keeps today's downgrade to the shared home.
func TestSpawnStillDowngradesOnAnOrdinaryMkdirError(t *testing.T) {
	t.Parallel()
	mgr, _ := recheckManager(t)
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	broken := []string{filepath.Join(file, "a"), filepath.Join(file, "b"), filepath.Join(file, "c")}
	mgr.HarnessHomeDir = broken[0]
	mgr.harnessChain = broken
	sess := sessionFor("coder", "codex")
	plan := mgr.planLaunch(sess)
	if plan.homeRefusal != "" {
		t.Errorf("an ordinary mkdir error refused the spawn: %s", plan.homeRefusal)
	}
	if sess.HarnessHome != "" {
		t.Errorf("home = %q, want none (the shared home)", sess.HarnessHome)
	}
	if plan.command == "" {
		t.Error("the seat was not given a launch command")
	}
}

func TestHarnessHomeRefusalStandsAndClears(t *testing.T) {
	t.Parallel()
	mgr := homeManager(t)
	sess := sessionFor("coder", "codex")
	err := mgr.refuseHarnessHome(sess, "no usable harness home base: x")
	if !errors.Is(err, ErrPlacementRefused) {
		t.Fatalf("err = %v, want ErrPlacementRefused", err)
	}
	got := mgr.PlacementRefusals()
	if len(got) != 1 || got[0].Reason != RefusalHarnessHome {
		t.Fatalf("refusals = %+v, want one %q", got, RefusalHarnessHome)
	}
	mgr.clearHarnessHomeRefusal(sess)
	if got := mgr.PlacementRefusals(); len(got) != 0 {
		t.Errorf("refusal outlived a good spawn: %+v", got)
	}
	_ = mgr.refuseHarnessHome(sess, "again")
	mgr.ClearPlacementRefusal(sess.Workspace, sess.Team, sess.Role)
	if got := mgr.PlacementRefusals(); len(got) != 0 {
		t.Errorf("ClearPlacementRefusal left %+v", got)
	}
}

// The Manager entry of the empty-base sweep consults the Manager's ownership
// seam. Both runs use a scratch root only.
func TestSweepEmptyBasesThroughTheManagerHonoursTheOwnershipSeam(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		owner       func(os.FileInfo) bool
		wantRemoved bool
	}{
		{"nil check removes an empty private base", nil, true},
		{"a refusing check keeps it", func(os.FileInfo) bool { return false }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr := homeManager(t)
			root := baseRoot(t)
			mgr.tempRoot = root
			mgr.ownerCheck = tc.owner
			base := mkTempBase(t, root, "marvel-h-bbbbbbbb", 0o700)
			sw := mgr.SweepEmptyBases(true)
			if sw.Root != root {
				t.Fatalf("swept %q, want the scratch root %q", sw.Root, root)
			}
			if got := !exists(base); got != tc.wantRemoved {
				t.Errorf("removed = %v, want %v (%+v)", got, tc.wantRemoved, sw)
			}
		})
	}
}

// A base is made with one Mkdir, never MkdirAll: a missing parent is an error,
// and nothing above the base is created on a caller's behalf.
func TestBaseIsNeverMadeWithMkdirAll(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	missing := filepath.Join(root, "missing")
	why, validation := privateDirRefusal(filepath.Join(missing, "base"), ownedByMe)
	if why == "" || validation {
		t.Errorf("why = %q, validation = %v; want an ordinary failure", why, validation)
	}
	if exists(missing) {
		t.Error("the parent of a base was created")
	}
}
