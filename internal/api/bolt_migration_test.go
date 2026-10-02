package api

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// The v1 to v2 store migration (docs/design/seat-bootstrap.md section 3, test
// 10). Every test builds its v1 file by hand, so the fixture does not depend on
// the writer this change replaces.

type v1Fixture struct {
	workspaces []Workspace
	teams      []Team
}

// writeV1 writes a bbolt file exactly as a schema-version-1 daemon left it.
func writeV1(t *testing.T, path string, f v1Fixture) {
	t.Helper()
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Update(func(tx *bolt.Tx) error {
		for _, name := range allBuckets {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return err
			}
		}
		if err := tx.Bucket(bucketMeta).Put(metaKeySchemaVersion, encodeUint64(1)); err != nil {
			return err
		}
		for _, w := range f.workspaces {
			b, _ := json.Marshal(w)
			if err := tx.Bucket(bucketWorkspaces).Put([]byte(w.Key()), b); err != nil {
				return err
			}
		}
		for _, tm := range f.teams {
			b, _ := json.Marshal(tm)
			if err := tx.Bucket(bucketTeams).Put([]byte(tm.Key()), b); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// readRaw reads a bbolt file without going through Store: its schema version
// and its team records as stored.
func readRaw(t *testing.T, path string) (uint64, map[string]Team) {
	t.Helper()
	db, err := bolt.Open(path, 0o600, &bolt.Options{ReadOnly: true, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	teams := map[string]Team{}
	var version uint64
	if err := db.View(func(tx *bolt.Tx) error {
		version = decodeUint64(tx.Bucket(bucketMeta).Get(metaKeySchemaVersion))
		return tx.Bucket(bucketTeams).ForEach(func(k, v []byte) error {
			var tm Team
			if err := json.Unmarshal(v, &tm); err != nil {
				return err
			}
			teams[string(k)] = tm
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	return version, teams
}

func standardV1() v1Fixture {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	return v1Fixture{
		workspaces: []Workspace{
			{Name: "legacy", CreatedAt: now},
			{Name: "rooted", Root: "/srv/rooted", CreatedAt: now},
		},
		teams: []Team{
			{
				Name: "squad", Workspace: "legacy", Generation: 3, CreatedAt: now,
				Roles: []Role{{Name: "worker", Replicas: 1}},
			},
			{
				Name: "declared", Workspace: "legacy", WorkDir: "/srv/declared", Generation: 1, CreatedAt: now,
				Roles: []Role{{Name: "worker", Replicas: 1}},
			},
			{
				Name: "squad", Workspace: "rooted", Generation: 1, CreatedAt: now,
				Roles: []Role{{Name: "worker", Replicas: 1}},
			},
		},
	}
}

func openMigrating(t *testing.T, path, cwd string) (*Store, error) {
	t.Helper()
	s := NewStore()
	err := s.OpenBoltWithOptions(path, BoltOptions{LegacyCwd: cwd})
	return s, err
}

func setFault(t *testing.T, stage string, err error) {
	t.Helper()
	boltMigrationFault = func(got string) error {
		if got == stage {
			return err
		}
		return nil
	}
	t.Cleanup(func() { boltMigrationFault = nil })
}

func TestMigrationStampsRootlessTeamsAndWritesV2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	writeV1(t, path, standardV1())
	cwdX := "/srv/orc-root"

	s, err := openMigrating(t, path, cwdX)
	if err != nil {
		t.Fatalf("open v1 store: %v", err)
	}
	stamps := s.LegacyStamps()
	if len(stamps) != 1 || stamps[0].TeamKey != "legacy/squad" || stamps[0].Dir != cwdX {
		t.Fatalf("LegacyStamps = %+v, want exactly legacy/squad at %s", stamps, cwdX)
	}
	stamped, err := s.GetTeam("legacy/squad")
	if err != nil || stamped.WorkDir != cwdX || stamped.WorkDirSource != "legacy" {
		t.Fatalf("legacy/squad = (%q, %q, %v), want %s stamped legacy", stamped.WorkDir, stamped.WorkDirSource, err, cwdX)
	}
	// A team that declared a workdir, and a team whose workspace has a root,
	// keep what they declared.
	if d, _ := s.GetTeam("legacy/declared"); d.WorkDir != "/srv/declared" || d.WorkDirSource != "" {
		t.Errorf("legacy/declared = (%q, %q), want its declared workdir untouched", d.WorkDir, d.WorkDirSource)
	}
	if r, _ := s.GetTeam("rooted/squad"); r.WorkDir != "" || r.WorkDirSource != "" {
		t.Errorf("rooted/squad = (%q, %q), want unstamped: its workspace has a root", r.WorkDir, r.WorkDirSource)
	}
	_ = s.CloseBolt()

	if v, teams := readRaw(t, path); v != 2 || teams["legacy/squad"].WorkDir != cwdX {
		t.Fatalf("on disk: version %d, legacy/squad workdir %q; want 2 and %s", v, teams["legacy/squad"].WorkDir, cwdX)
	}
}

func TestMigrationTakesAPrivateBackupOfTheV1Store(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	writeV1(t, path, standardV1())
	_, before := readRaw(t, path)

	s, err := openMigrating(t, path, "/srv/orc-root")
	if err != nil {
		t.Fatal(err)
	}
	_ = s.CloseBolt()

	bak := path + ".v1.bak"
	info, err := os.Stat(bak)
	if err != nil {
		t.Fatalf("no backup at %s: %v", bak, err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("backup mode = %v, want 0600", info.Mode().Perm())
	}
	v, got := readRaw(t, bak)
	if v != 1 {
		t.Fatalf("backup schema version = %d, want 1", v)
	}
	if len(got) != len(before) {
		t.Fatalf("backup holds %d teams, want the %d before migration", len(got), len(before))
	}
	for k, want := range before {
		a, _ := json.Marshal(want)
		b, _ := json.Marshal(got[k])
		if string(a) != string(b) {
			t.Errorf("backup record %s differs from the pre-migration record", k)
		}
	}
}

// A restart from another directory does not move the stamp, and says nothing.
func TestMigrationStampSurvivesRestartFromAnotherDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	writeV1(t, path, standardV1())
	s, err := openMigrating(t, path, "/srv/orc-root")
	if err != nil {
		t.Fatal(err)
	}
	_ = s.CloseBolt()

	s2, err := openMigrating(t, path, "/somewhere/else")
	if err != nil {
		t.Fatalf("reopen v2: %v", err)
	}
	defer func() { _ = s2.CloseBolt() }()
	if got, _ := s2.GetTeam("legacy/squad"); got.WorkDir != "/srv/orc-root" {
		t.Errorf("stamp moved to %q on a restart from another directory", got.WorkDir)
	}
	if n := len(s2.LegacyStamps()); n != 0 {
		t.Errorf("a second open reported %d stamps, want none", n)
	}
}

func TestMigrationFreshStoreIsV2WithNoBackupAndNoStamps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	s, err := openMigrating(t, path, "/srv/orc-root")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(s.LegacyStamps()); n != 0 {
		t.Errorf("fresh store reported %d stamps", n)
	}
	_ = s.CloseBolt()
	if v, _ := readRaw(t, path); v != 2 {
		t.Errorf("fresh store schema version = %d, want 2", v)
	}
	if _, err := os.Stat(path + ".v1.bak"); err == nil {
		t.Error("a fresh store took a v1 backup")
	}
}

// A failure in the middle of the stamp pass leaves the store at v1 with nothing
// stamped. The backup was already written, so the next start refuses and names
// it; once it is moved aside, the next start takes a fresh backup and migrates.
func TestMigrationMidPassFailureLeavesV1AndTheNextStartRefuses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	writeV1(t, path, standardV1())
	setFault(t, "stamp", errors.New("injected stamp failure"))

	if _, err := openMigrating(t, path, "/srv/orc-root"); err == nil {
		t.Fatal("open succeeded despite a failure mid-pass")
	}
	v, teams := readRaw(t, path)
	if v != 1 || teams["legacy/squad"].WorkDir != "" {
		t.Fatalf("after a failed pass: version %d, legacy/squad workdir %q; want v1 and nothing stamped", v, teams["legacy/squad"].WorkDir)
	}
	bak := path + ".v1.bak"
	if _, err := os.Stat(bak); err != nil {
		t.Fatalf("the backup written before the pass is missing: %v", err)
	}

	boltMigrationFault = nil
	_, err := openMigrating(t, path, "/srv/orc-root")
	if err == nil || !strings.Contains(err.Error(), bak) || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("restart error = %v, want the interrupted-migration refusal naming %s", err, bak)
	}
	if v, _ := readRaw(t, path); v != 1 {
		t.Fatalf("the refused start changed the store to version %d", v)
	}

	if err := os.Rename(bak, bak+".moved"); err != nil {
		t.Fatal(err)
	}
	s, err := openMigrating(t, path, "/srv/orc-root")
	if err != nil {
		t.Fatalf("start after moving the backup aside: %v", err)
	}
	_ = s.CloseBolt()
	if v, _ := readRaw(t, path); v != 2 {
		t.Errorf("version = %d after the recovered start, want 2", v)
	}
	if _, err := os.Stat(bak); err != nil {
		t.Errorf("the recovered start took no fresh backup: %v", err)
	}
}

func TestMigrationBackupFailureFailsOpenAndStampsNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	writeV1(t, path, standardV1())
	setFault(t, "backup", errors.New("injected backup failure"))

	if _, err := openMigrating(t, path, "/srv/orc-root"); err == nil {
		t.Fatal("open succeeded though the backup could not be written")
	}
	v, teams := readRaw(t, path)
	if v != 1 || teams["legacy/squad"].WorkDir != "" {
		t.Fatalf("version %d, workdir %q; want v1 and nothing stamped", v, teams["legacy/squad"].WorkDir)
	}
	if _, err := os.Stat(path + ".v1.bak"); err == nil {
		t.Error("a failed backup left a file behind that the next start would refuse")
	}
}

// An existing backup is never overwritten: the daemon refuses and says how to
// recover.
func TestMigrationRefusesWhenABackupAlreadyExists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	writeV1(t, path, standardV1())
	bak := path + ".v1.bak"
	if err := os.WriteFile(bak, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := openMigrating(t, path, "/srv/orc-root")
	if err == nil || !strings.Contains(err.Error(), bak) || !strings.Contains(err.Error(), "Move it aside") {
		t.Fatalf("error = %v, want the refusal naming %s and how to recover", err, bak)
	}
	if b, _ := os.ReadFile(bak); string(b) != "stale" {
		t.Errorf("the existing backup was modified: %q", b)
	}
	if v, _ := readRaw(t, path); v != 1 {
		t.Errorf("version = %d, want the store left at v1", v)
	}
}

func TestMigrationRefusesANewerStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	writeV1(t, path, standardV1())
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketMeta).Put(metaKeySchemaVersion, encodeUint64(99))
	}); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, err := openMigrating(t, path, "/srv/orc-root"); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("error = %v, want the newer-than-binary refusal", err)
	}
}

const rootlessTOML = `
[workspace]
name = "wd"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    command = "claude"
`

// stampedStore applies a root-less manifest, then marks its team the way the
// migration would have.
func stampedStore(t *testing.T) *Store {
	t.Helper()
	store := NewStore()
	m, err := ParseManifestBytes([]byte(rootlessTOML))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(store); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateTeam("wd/squad", func(live *Team) error {
		live.WorkDir = "/srv/orc-root"
		live.WorkDirSource = WorkDirSourceLegacy
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return store
}

// A re-apply that still names no root keeps the stamp: the team stays where
// its seats are, and the operator is told to declare a root.
func TestReapplyWithoutARootKeepsTheLegacyStamp(t *testing.T) {
	store := stampedStore(t)
	m, _ := ParseManifestBytes([]byte(rootlessTOML))
	notes := m.LegacyPlacementNotes(store)
	if err := m.Apply(store); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetTeam("wd/squad")
	if got.WorkDir != "/srv/orc-root" || got.WorkDirSource != WorkDirSourceLegacy {
		t.Fatalf("after a root-less re-apply: (%q, %q), want the stamp kept", got.WorkDir, got.WorkDirSource)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "placement: legacy daemon cwd") || !strings.Contains(notes[0], "/srv/orc-root") || !strings.Contains(notes[0], "declare a root") {
		t.Fatalf("notes = %q, want one placement note naming the legacy directory and asking for a root", notes)
	}
}

// A re-apply that declares a root replaces the stamp, so placement follows the
// declaration from then on.
func TestReapplyWithARootReplacesTheLegacyStamp(t *testing.T) {
	store := stampedStore(t)
	m, _ := ParseManifestBytes([]byte(rootlessTOML))
	m.Workspace.Root = "/work/proj"
	if notes := m.LegacyPlacementNotes(store); len(notes) != 0 {
		t.Errorf("a re-apply that declares a root still got notes: %q", notes)
	}
	if err := m.Apply(store); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetTeam("wd/squad")
	if got.WorkDir != "" || got.WorkDirSource != "" {
		t.Fatalf("after a re-apply with a root: (%q, %q), want the stamp replaced", got.WorkDir, got.WorkDirSource)
	}
}

// A re-apply that declares the team's own workdir replaces the stamp too.
func TestReapplyWithATeamWorkdirReplacesTheLegacyStamp(t *testing.T) {
	store := stampedStore(t)
	m, _ := ParseManifestBytes([]byte(rootlessTOML))
	m.Teams[0].WorkDir = "/srv/declared"
	if err := m.Apply(store); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetTeam("wd/squad")
	if got.WorkDir != "/srv/declared" || got.WorkDirSource != "" {
		t.Fatalf("after declaring a team workdir: (%q, %q), want /srv/declared and no source", got.WorkDir, got.WorkDirSource)
	}
}
