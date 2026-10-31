package session

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
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
	mgr.skipBaseCheck = false
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

// A leaf is made with one Mkdir at 0700 and then checked, never MkdirAll: a
// symlinked leaf is not followed, and a leaf of the wrong mode or owner is not
// adopted. An existing private leaf is reused, so a restart lands in the same
// home.
func TestSpawnRefusesASymlinkedLeaf(t *testing.T) {
	t.Parallel()
	mgr, b := recheckManager(t)
	sess := sessionFor("coder", "codex")
	target := t.TempDir()
	if err := os.Chmod(target, 0o700); err != nil {
		t.Fatal(err)
	}
	leaf := filepath.Base(mgr.harnessHomeFor(sess.Key()))
	if err := os.Symlink(target, filepath.Join(b.durable, leaf)); err != nil {
		t.Fatal(err)
	}
	plan := mgr.planLaunch(sess)
	if plan.homeRefusal == "" || !strings.Contains(plan.homeRefusal, "not a plain directory") {
		t.Errorf("refusal = %q, want a refusal naming the link", plan.homeRefusal)
	}
	if sess.HarnessHome != "" {
		t.Errorf("a home was recorded through the link: %q", sess.HarnessHome)
	}
	if n := entries(t, target); n != 0 {
		t.Errorf("the link target holds %d entries, want 0", n)
	}
}

func TestSpawnRefusesALeafOfTheWrongMode(t *testing.T) {
	t.Parallel()
	mgr, b := recheckManager(t)
	sess := sessionFor("coder", "codex")
	leaf := filepath.Join(b.durable, filepath.Base(mgr.harnessHomeFor(sess.Key())))
	plant(t, leaf)
	plan := mgr.planLaunch(sess)
	if plan.homeRefusal == "" || !strings.Contains(plan.homeRefusal, "want 0700") {
		t.Errorf("refusal = %q, want a refusal naming the mode", plan.homeRefusal)
	}
	if sess.HarnessHome != "" {
		t.Errorf("a wide leaf was adopted: %q", sess.HarnessHome)
	}
}

func TestSpawnRefusesALeafAnotherUserOwns(t *testing.T) {
	t.Parallel()
	mgr, b := recheckManager(t)
	sess := sessionFor("coder", "codex")
	leaf := filepath.Base(mgr.harnessHomeFor(sess.Key()))
	mkPrivate(t, filepath.Join(b.durable, leaf))
	mgr.ownerCheck = func(fi os.FileInfo) bool { return fi.Name() != leaf }
	plan := mgr.planLaunch(sess)
	if plan.homeRefusal == "" || !strings.Contains(plan.homeRefusal, "not owned by this user") {
		t.Errorf("refusal = %q, want a refusal naming the owner", plan.homeRefusal)
	}
}

func TestSpawnReusesAnExistingPrivateLeaf(t *testing.T) {
	t.Parallel()
	mgr, b := recheckManager(t)
	first := sessionFor("coder", "codex")
	mgr.planLaunch(first)
	marker := filepath.Join(first.HarnessHome, "kept")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	again := sessionFor("coder", "codex")
	if plan := mgr.planLaunch(again); plan.homeRefusal != "" {
		t.Fatalf("refused an existing private home: %s", plan.homeRefusal)
	}
	if again.HarnessHome != first.HarnessHome || !underBase(again.HarnessHome, b.durable) || !exists(marker) {
		t.Errorf("home = %q (first %q), marker kept = %v", again.HarnessHome, first.HarnessHome, exists(marker))
	}
}

// The base vanishes after the check and before the leaf is made. That is the
// race the check closes: the selection runs again and the home lands under a
// base that passes, or the spawn is refused. It is never the operator's home.
func TestSpawnReselectsWhenTheBaseVanishesOnce(t *testing.T) {
	t.Parallel()
	mgr, b := recheckManager(t)
	calls := 0
	mgr.baseChecked = func(base string) {
		calls++
		if calls == 1 {
			if err := os.Remove(base); err != nil {
				t.Error(err)
			}
		}
	}
	sess := sessionFor("coder", "codex")
	plan := mgr.planLaunch(sess)
	if plan.homeRefusal != "" {
		t.Fatalf("refused after one vanish: %s", plan.homeRefusal)
	}
	if calls != 2 || !underBase(sess.HarnessHome, b.durable) {
		t.Errorf("calls = %d, home = %q; want a second selection and a home under %q", calls, sess.HarnessHome, b.durable)
	}
	if fi, err := os.Lstat(b.durable); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("the base was not recreated at 0700: %v %v", fi, err)
	}
}

func TestSpawnRefusesWhenTheBaseVanishesTwice(t *testing.T) {
	t.Parallel()
	mgr, _ := recheckManager(t)
	mgr.baseChecked = func(base string) {
		if err := os.Remove(base); err != nil {
			t.Error(err)
		}
	}
	sess := sessionFor("coder", "codex")
	plan := mgr.planLaunch(sess)
	if !strings.Contains(plan.homeRefusal, "vanished") {
		t.Errorf("refusal = %q, want one naming the vanished base", plan.homeRefusal)
	}
	if sess.HarnessHome != "" || plan.env["CODEX_HOME"] != "" {
		t.Errorf("a refused spawn still got a home: %q", sess.HarnessHome)
	}
}

// The check is on by default, and NewManager does not turn it off.
func TestNewManagerChecksTheBaseAtSpawn(t *testing.T) {
	mgr := NewManager(api.NewStore(), nil)
	if mgr.skipBaseCheck {
		t.Error("NewManager built a Manager that skips the spawn-time base check")
	}
	if len(mgr.harnessChain) == 0 || !slices.Contains(mgr.harnessChain, mgr.HarnessHomeDir) {
		t.Errorf("chain = %v, want it to hold the chosen base %q", mgr.harnessChain, mgr.HarnessHomeDir)
	}
}

// Create refuses a spawn whose every base fails validation: an error wrapping
// ErrPlacementRefused, no session row, nothing started, and a standing
// refusal that is still one record after another try. Fixing the directories
// lets the next Create through and clears the refusal.
func TestCreateRefusesWhenNoHarnessHomeBaseIsUsableAndClearsOnceItIs(t *testing.T) {
	r := newContainerRig(t, "test-hh-refuse", api.PlacementLocalhost, api.Role{Name: "seat", Replicas: 1})
	root := t.TempDir()
	b := recheckBases{durable: filepath.Join(root, "d"), legacy: filepath.Join(root, "l"), fallback: filepath.Join(root, "f")}
	for _, p := range []string{b.durable, b.legacy, b.fallback} {
		plant(t, p)
	}
	r.mgr.HarnessHomeDir = b.durable
	r.mgr.harnessChain = []string{b.durable, b.legacy, b.fallback}
	sess := r.session("codex")

	err := r.mgr.Create(sess)
	if !errors.Is(err, ErrPlacementRefused) {
		t.Fatalf("Create = %v, want an error wrapping ErrPlacementRefused", err)
	}
	if _, gerr := r.store.GetSession(sess.Key()); gerr == nil {
		t.Error("a refused spawn left a session record")
	}
	if !r.neverRan(t) {
		t.Error("the seat started")
	}
	standing := func() []PlacementRefusal {
		var out []PlacementRefusal
		for _, p := range r.mgr.PlacementRefusals() {
			if p.Reason == RefusalHarnessHome {
				out = append(out, p)
			}
		}
		return out
	}
	first := standing()
	if len(first) != 1 {
		t.Fatalf("standing refusals = %+v, want one %q", first, RefusalHarnessHome)
	}
	if err := r.mgr.Create(r.session("codex")); !errors.Is(err, ErrPlacementRefused) {
		t.Fatalf("second Create = %v, want the refusal again", err)
	}
	if again := standing(); len(again) != 1 || !again[0].Since.Equal(first[0].Since) {
		t.Errorf("the refusal did not stay in place: %+v then %+v", first, again)
	}

	for _, p := range []string{b.durable, b.legacy, b.fallback} {
		if err := os.Chmod(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.mgr.Create(r.session("codex")); err != nil {
		t.Fatalf("Create after the directories were fixed = %v", err)
	}
	if left := standing(); len(left) != 0 {
		t.Errorf("the refusal outlived a good spawn: %+v", left)
	}
}
