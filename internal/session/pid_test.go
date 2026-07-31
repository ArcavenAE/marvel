package session

import (
	"strconv"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/tmux"
)

// Session.PID is what the process sampler walks. Before it was
// populated, api.Session carried the field and nothing ever assigned it,
// so every resource reading was over an empty pid set.
func TestCreateRecordsPanePID(t *testing.T) {
	skipIfNoTmux(t)

	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	mgr := NewManager(store, driver)

	ws := "test-pid-spawn"
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })

	sess := &api.Session{
		Name:      "agent-0",
		Workspace: ws,
		Team:      "agents",
		Role:      "worker",
		Runtime:   api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
	}
	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	if sess.PID <= 0 {
		t.Errorf("returned session PID = %d, want a positive pid", sess.PID)
	}
	stored, err := store.GetSession(ws + "/agent-0")
	if err != nil {
		t.Fatalf("get from store: %v", err)
	}
	if stored.PID != sess.PID {
		t.Errorf("store PID = %d, returned PID = %d; the two must agree", stored.PID, sess.PID)
	}

	// The pid must be tmux's pane_pid, not marvel's own or the tmux
	// server's.
	panes, err := driver.ListPanes("marvel-" + ws)
	if err != nil {
		t.Fatalf("list panes: %v", err)
	}
	var want string
	for _, p := range panes {
		if p.ID == sess.PaneID {
			want = p.PID
		}
	}
	if want != strconv.Itoa(stored.PID) {
		t.Errorf("stored PID %d does not match pane_pid %q", stored.PID, want)
	}
}

// A daemon restart rehydrates sessions from bolt and adopts their panes.
// AdoptOrKill has pane_pid in hand from ListPanes and used to drop it,
// which left adopted sessions unsampleable for as long as they ran.
func TestAdoptOrKillRecordsPanePID(t *testing.T) {
	skipIfNoTmux(t)

	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	mgr := NewManager(store, driver)

	ws := "test-pid-adopt"
	key := ws + "/agent-0"
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })

	if err := store.CreateWorkspace(&api.Workspace{Name: ws}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	sess := &api.Session{
		Name:      "agent-0",
		Workspace: ws,
		Team:      "agents",
		Role:      "worker",
		Runtime:   api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
	}
	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	spawnPID := sess.PID

	// Stand in for a record written before marvel populated PID at all.
	if err := store.UpdateSession(key, func(live *api.Session) error {
		live.PID = 0
		return nil
	}); err != nil {
		t.Fatalf("clear PID: %v", err)
	}

	adopted, _, err := mgr.AdoptOrKill()
	if err != nil {
		t.Fatalf("AdoptOrKill: %v", err)
	}
	if adopted < 1 {
		t.Fatalf("adopted %d panes, want at least 1", adopted)
	}

	stored, err := store.GetSession(key)
	if err != nil {
		t.Fatalf("get from store: %v", err)
	}
	if stored.PID != spawnPID {
		t.Errorf("PID after adopt = %d, want %d (the pane's pid, recovered from ListPanes)",
			stored.PID, spawnPID)
	}
}
