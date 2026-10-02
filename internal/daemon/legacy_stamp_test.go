package daemon

import (
	"encoding/binary"
	"os"
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

// A linked git worktree has a .git FILE (a pointer), a normal checkout a .git
// directory. The walk goes up to the first .git it finds.
func TestLinkedWorktree(t *testing.T) {
	t.Parallel()
	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /elsewhere/.git/worktrees/x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(wt, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	main := t.TempDir()
	if err := os.Mkdir(filepath.Join(main, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(main, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	plain := t.TempDir()
	cases := []struct {
		name string
		dir  string
		want bool
	}{
		{"the worktree root", wt, true},
		{"a subdirectory of a worktree", filepath.Join(wt, "a", "b"), true},
		{"a normal checkout", main, false},
		{"a subdirectory of a normal checkout", filepath.Join(main, "sub"), false},
		{"no repository at all", plain, false},
		{"a directory that does not exist", filepath.Join(plain, "gone"), false},
	}
	for _, tc := range cases {
		if got := linkedWorktree(tc.dir); got != tc.want {
			t.Errorf("%s: linkedWorktree(%q) = %v, want %v", tc.name, tc.dir, got, tc.want)
		}
	}
}

// The announcement says so when the pinned directory is a linked worktree, in
// the event and the log: if it is removed later, new-window -c starts the seat
// in $HOME without an error. It warns and does not refuse.
func TestStampAnnouncementWarnsOfALinkedWorktree(t *testing.T) {
	skipIfNoTmux(t)
	path := forgeV1Store(t)
	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /elsewhere/.git/worktrees/x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ring := events.NewRing(events.DefaultCapacity)
	d, err := NewWithOptions(Options{StateBolt: path, Events: ring, LegacyCwd: wt})
	if err != nil {
		t.Fatalf("a worktree directory must warn, not refuse: %v", err)
	}
	defer func() { _ = d.store.CloseBolt() }()
	got := stampEvents(ring)
	if len(got) != 1 || !strings.Contains(got[0].Message, "linked git worktree") {
		t.Fatalf("events = %+v, want one that names the linked git worktree", got)
	}
	if got[0].Severity != events.SeverityWarning {
		t.Errorf("severity = %v, want warning", got[0].Severity)
	}
}

// A .git FILE is not enough to call a directory a linked worktree: a submodule
// and a checkout made with --separate-git-dir have one too. What a worktree's
// file points at is `<repo>/.git/worktrees/<name>`.
func TestLinkedWorktreeTellsWorktreesFromSubmodules(t *testing.T) {
	t.Parallel()
	gitFile := func(content string) string {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".git"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	cases := []struct {
		name string
		file string
		want bool
	}{
		{"a worktree, absolute gitdir", "gitdir: /r/main/.git/worktrees/feature\n", true},
		{"a worktree, relative gitdir", "gitdir: ../main/.git/worktrees/feature\n", true},
		{"a worktree of a submodule", "gitdir: /r/main/.git/modules/sub/worktrees/y\n", true},
		{"a submodule", "gitdir: ../.git/modules/sub\n", false},
		{"a nested submodule", "gitdir: ../../.git/modules/a/modules/b\n", false},
		{"a separate git dir", "gitdir: /elsewhere/repo.git\n", false},
		{"an unreadable pointer", "not a gitdir line\n", false},
	}
	for _, tc := range cases {
		if got := linkedWorktree(gitFile(tc.file)); got != tc.want {
			t.Errorf("%s: linkedWorktree = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A .git that is a symlink to a directory is an ordinary checkout, and a
// symlinked cwd is judged by where it points, not by the link's own parent.
func TestLinkedWorktreeFollowsSymlinks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	realGit := filepath.Join(root, "real.git")
	if err := os.Mkdir(realGit, 0o755); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "checkout")
	if err := os.Mkdir(linked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realGit, filepath.Join(linked, ".git")); err != nil {
		t.Fatal(err)
	}
	if linkedWorktree(linked) {
		t.Error("a .git symlink to a directory was taken for a linked worktree")
	}

	wt := filepath.Join(root, "wt")
	if err := os.Mkdir(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /r/main/.git/worktrees/x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(wt, "pkg", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	link := filepath.Join(elsewhere, "cwd")
	if err := os.Symlink(sub, link); err != nil {
		t.Fatal(err)
	}
	if !linkedWorktree(link) {
		t.Error("a symlinked cwd that points into a subdirectory of a linked worktree was not recognised")
	}
}
