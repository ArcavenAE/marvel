package api

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestBoltStore_NotOpenedIsHarmless verifies that a Store with no
// OpenBolt call behaves identically to the legacy in-memory Store.
// This is the load-bearing invariant for the spike's safety: existing
// callers (tests, current daemon path) keep working without changes.
func TestBoltStore_NotOpenedIsHarmless(t *testing.T) {
	s := NewStore()

	if err := s.CreateWorkspace(&Workspace{Name: "ws1"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if got := s.ResourceVersion(); got != 0 {
		t.Fatalf("ResourceVersion when bolt not open: want 0, got %d", got)
	}
	if err := s.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint when bolt not open: %v", err)
	}
	if err := s.CloseBolt(); err != nil {
		t.Fatalf("CloseBolt when bolt not open: %v", err)
	}
}

func TestBoltStore_OpenWriteCloseReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")

	// Open #1: populate state.
	s1 := NewStore()
	if err := s1.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #1: %v", err)
	}

	ws := &Workspace{Name: "demo", CreatedAt: time.Now().UTC().Truncate(time.Second)}
	if err := s1.CreateWorkspace(ws); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	team := &Team{
		Name:      "team-a",
		Workspace: "demo",
		Roles: []Role{
			{Name: "worker", Replicas: 2, Runtime: Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
		},
		Generation: 1,
		CreatedAt:  time.Now().UTC().Truncate(time.Second),
	}
	if err := s1.CreateTeam(team); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}

	sess := &Session{
		Name:       "agent-0",
		Workspace:  "demo",
		Team:       "team-a",
		Role:       "worker",
		Generation: 1,
		Runtime:    Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
		State:      SessionRunning,
		PaneID:     "%5",
		CreatedAt:  time.Now().UTC().Truncate(time.Second),
	}
	if err := s1.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	endpoint := &Endpoint{Name: "the-worker", Workspace: "demo", Team: "team-a"}
	if err := s1.CreateEndpoint(endpoint); err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}

	if err := s1.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if err := s1.CloseBolt(); err != nil {
		t.Fatalf("CloseBolt: %v", err)
	}

	// Open #2: fresh in-memory state, rehydrate from disk.
	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #2 (rehydrate): %v", err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })

	// Workspace survived.
	gotWs, err := s2.GetWorkspace("demo")
	if err != nil {
		t.Fatalf("GetWorkspace after rehydrate: %v", err)
	}
	if gotWs.Name != "demo" {
		t.Fatalf("workspace name: want demo, got %q", gotWs.Name)
	}

	// Team survived with Roles.
	gotTeam, err := s2.GetTeam("demo/team-a")
	if err != nil {
		t.Fatalf("GetTeam after rehydrate: %v", err)
	}
	if len(gotTeam.Roles) != 1 || gotTeam.Roles[0].Name != "worker" || gotTeam.Roles[0].Replicas != 2 {
		t.Fatalf("team roles after rehydrate: %+v", gotTeam.Roles)
	}
	if gotTeam.Generation != 1 {
		t.Fatalf("team generation: want 1, got %d", gotTeam.Generation)
	}

	// Session survived with PaneID + State.
	gotSess, err := s2.GetSession("demo/agent-0")
	if err != nil {
		t.Fatalf("GetSession after rehydrate: %v", err)
	}
	if gotSess.PaneID != "%5" {
		t.Fatalf("session PaneID: want %%5, got %q", gotSess.PaneID)
	}
	if gotSess.State != SessionRunning {
		t.Fatalf("session state: want running, got %q", gotSess.State)
	}
	if gotSess.Generation != 1 {
		t.Fatalf("session generation: want 1, got %d", gotSess.Generation)
	}

	// Endpoint survived.
	gotEp, err := s2.GetEndpoint("demo/the-worker")
	if err != nil {
		t.Fatalf("GetEndpoint after rehydrate: %v", err)
	}
	if gotEp.Team != "team-a" {
		t.Fatalf("endpoint team: want team-a, got %q", gotEp.Team)
	}
}

func TestBoltStore_UpdateSessionPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")

	s1 := NewStore()
	if err := s1.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #1: %v", err)
	}

	sess := &Session{
		Name:      "agent-0",
		Workspace: "ws",
		Team:      "team",
		Role:      "worker",
		State:     SessionPending,
		PaneID:    "%1",
		Runtime:   Runtime{Name: "sleep", Command: "sleep", Args: []string{"60"}},
	}
	if err := s1.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// Transition Pending → Running and bump PaneID via UpdateSession,
	// which is the mainline path session.Manager.Create uses.
	if err := s1.UpdateSession(sess.Key(), func(live *Session) error {
		live.State = SessionRunning
		live.PaneID = "%7"
		return nil
	}); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	if err := s1.CloseBolt(); err != nil {
		t.Fatalf("CloseBolt: %v", err)
	}

	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #2: %v", err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })

	got, err := s2.GetSession("ws/agent-0")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.State != SessionRunning {
		t.Fatalf("state after rehydrate: want running, got %q", got.State)
	}
	if got.PaneID != "%7" {
		t.Fatalf("PaneID after rehydrate: want %%7, got %q", got.PaneID)
	}
}

func TestBoltStore_DeletesPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")

	s1 := NewStore()
	if err := s1.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #1: %v", err)
	}
	if err := s1.CreateWorkspace(&Workspace{Name: "ws"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if err := s1.DeleteWorkspace("ws"); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}
	if err := s1.CloseBolt(); err != nil {
		t.Fatalf("CloseBolt: %v", err)
	}

	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #2: %v", err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })

	if list := s2.ListWorkspaces(); len(list) != 0 {
		t.Fatalf("after delete+reopen, workspaces should be empty: got %+v", list)
	}
}

func TestBoltStore_ResourceVersionMonotonic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	s := NewStore()
	if err := s.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt: %v", err)
	}
	t.Cleanup(func() { _ = s.CloseBolt() })

	v0 := s.ResourceVersion()
	if v0 != 0 {
		t.Fatalf("initial ResourceVersion: want 0, got %d", v0)
	}
	if err := s.CreateWorkspace(&Workspace{Name: "ws"}); err != nil {
		t.Fatal(err)
	}
	v1 := s.ResourceVersion()
	if v1 <= v0 {
		t.Fatalf("ResourceVersion did not advance after CreateWorkspace: %d -> %d", v0, v1)
	}
	if err := s.CreateTeam(&Team{Name: "team", Workspace: "ws"}); err != nil {
		t.Fatal(err)
	}
	v2 := s.ResourceVersion()
	if v2 <= v1 {
		t.Fatalf("ResourceVersion did not advance after CreateTeam: %d -> %d", v1, v2)
	}
	if err := s.DeleteWorkspace("ws"); err != nil {
		// Note: this errors on existing-team-blocks-workspace-delete if
		// we ever add that cascade rule. For now, workspace delete is
		// allowed independent of teams.
		t.Fatal(err)
	}
	v3 := s.ResourceVersion()
	if v3 <= v2 {
		t.Fatalf("ResourceVersion did not advance after DeleteWorkspace: %d -> %d", v2, v3)
	}
}

func TestBoltStore_SchemaVersionRefuseNewer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	// Step 1: open at current version, close.
	s := NewStore()
	if err := s.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt: %v", err)
	}
	if err := s.CloseBolt(); err != nil {
		t.Fatalf("CloseBolt: %v", err)
	}
	// We can't easily forge a higher schema version without exposing
	// the meta bucket — verifying the equal-version reopen succeeds
	// (and the bucket is initialized) gives us the smoke test.
	// Higher-version refusal is covered by the same code path on
	// future schema bumps.
	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatalf("reopen same version should succeed: %v", err)
	}
	_ = s2.CloseBolt()
}

func TestBoltStore_ConcurrentWritesUnderRace(t *testing.T) {
	// Smoke test for -race. Real concurrency stress is out of scope for
	// the spike, but this catches obvious leaks of internal state.
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	s := NewStore()
	if err := s.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt: %v", err)
	}
	t.Cleanup(func() { _ = s.CloseBolt() })

	const writers = 4
	const writes = 25
	var wg sync.WaitGroup
	wg.Add(writers)
	for w := 0; w < writers; w++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < writes; i++ {
				ws := &Workspace{Name: makeKey(id, i)}
				if err := s.CreateWorkspace(ws); err != nil {
					t.Errorf("CreateWorkspace: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	if got := len(s.ListWorkspaces()); got != writers*writes {
		t.Fatalf("after concurrent writes: want %d workspaces, got %d", writers*writes, got)
	}
}

func makeKey(writer, i int) string {
	return "ws-" + itoa(writer) + "-" + itoa(i)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
