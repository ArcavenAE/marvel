package daemon

import (
	"encoding/binary"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// forgeV1Store writes a store with one root-less team, then rewrites its
// schema version to 1, which is the file a pre-migration daemon left behind.
func forgeV1Store(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	s := api.NewStore()
	if err := s.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateWorkspace(&api.Workspace{Name: "legacy"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateTeam(&api.Team{Name: "squad", Workspace: "legacy", Generation: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseBolt(); err != nil {
		t.Fatal(err)
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	v := make([]byte, 8)
	binary.BigEndian.PutUint64(v, 1)
	if err := db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("meta")).Put([]byte("schema_version"), v)
	}); err != nil {
		t.Fatal(err)
	}
	return path
}

func stampEvents(ring *events.Ring) []events.Event {
	return ring.Snapshot(events.Filter{Kind: events.KindPlacementLegacyStamped}, 50)
}

// The migration's stamps are announced after the commit, one event per team,
// and again at every later start from the persisted field; a start from
// another directory moves nothing.
func TestMigrationAnnouncesEachStampOnceAndTheDirectoryIsFrozen(t *testing.T) {
	skipIfNoTmux(t)
	path := forgeV1Store(t)

	ring := events.NewRing(events.DefaultCapacity)
	d, err := NewWithOptions(Options{StateBolt: path, Events: ring, LegacyCwd: "/srv/orc-root"})
	if err != nil {
		t.Fatalf("start on a v1 store: %v", err)
	}
	got := stampEvents(ring)
	if len(got) != 1 || got[0].Workspace != "legacy" || got[0].Team != "squad" || !strings.Contains(got[0].Message, "/srv/orc-root") {
		t.Fatalf("events = %+v, want one placement.legacy-stamped for legacy/squad naming /srv/orc-root", got)
	}
	team, err := d.store.GetTeam("legacy/squad")
	if err != nil || team.WorkDir != "/srv/orc-root" || team.WorkDirSource != api.WorkDirSourceLegacy {
		t.Fatalf("team = (%q, %q, %v), want stamped /srv/orc-root legacy", team.WorkDir, team.WorkDirSource, err)
	}
	if err := d.store.CloseBolt(); err != nil {
		t.Fatal(err)
	}

	ring2 := events.NewRing(events.DefaultCapacity)
	d2, err := NewWithOptions(Options{StateBolt: path, Events: ring2, LegacyCwd: "/somewhere/else"})
	if err != nil {
		t.Fatalf("second start: %v", err)
	}
	defer func() { _ = d2.store.CloseBolt() }()
	// Announced from the persisted field at every start, so a crash after the
	// commit but before the event loses nothing.
	if got := stampEvents(ring2); len(got) != 1 || !strings.Contains(got[0].Message, "/srv/orc-root") {
		t.Errorf("a second start announced %+v, want the one stamp at /srv/orc-root again", got)
	}
	if team, _ := d2.store.GetTeam("legacy/squad"); team.WorkDir != "/srv/orc-root" {
		t.Errorf("the stamp moved to %q on a start from another directory", team.WorkDir)
	}
}

const legacyReapplyManifest = `
[workspace]
name = "legacy"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    command = "sleep"
    args = ["300"]
`

// A root-less re-apply of a stamped team says where it stays and what to do.
func TestRootlessReapplyOfAStampedTeamSaysSo(t *testing.T) {
	skipIfNoTmux(t)
	path := forgeV1Store(t)
	d, err := NewWithOptions(Options{StateBolt: path, LegacyCwd: "/srv/orc-root"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = d.sessMgr.CleanupWorkspace("legacy")
		_ = d.store.CloseBolt()
	})
	resp := applyManifest(t, d, legacyReapplyManifest)
	if resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
	if !strings.Contains(string(resp.Result), "placement: legacy daemon cwd /srv/orc-root; declare a root") {
		t.Fatalf("apply result = %s, want the legacy placement note", resp.Result)
	}
}
