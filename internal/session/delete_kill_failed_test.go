package session

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/runtime"
)

// errKillRefused stands in for a kill that tmux did not carry out while the
// pane may still be there: a wedged server, a permissions change, a timeout.
var errKillRefused = errors.New("kill-pane %7: server busy")

// failingPanes is a PaneController whose kill is refused. NewPane hands back
// a fixed pane id so the instance reaches StateRunning.
type failingPanes struct {
	mu     sync.Mutex
	killed []string
}

func (f *failingPanes) NewPane(_, _, _ string, _ map[string]string, _ bool) (string, error) {
	return "%7", nil
}

func (f *failingPanes) KillPane(paneID string) error {
	f.mu.Lock()
	f.killed = append(f.killed, paneID)
	f.mu.Unlock()
	return errKillRefused
}

func (f *failingPanes) SendKeys(string, string, bool, bool) error { return nil }
func (f *failingPanes) CapturePane(string) (string, error)        { return "", nil }

// keptRowFixture stores one replica row the way the reconciler names it, so
// the assertions can read it back through the same query nextIndex uses.
func keptRowFixture(t *testing.T) (*api.Store, *Manager, *events.Ring, api.Session) {
	t.Helper()
	store := api.NewStore()
	mgr := NewManager(store, nil)
	ring := events.NewRing(32)
	mgr.Events = ring
	sess := &api.Session{
		Name:       "squad-worker-g0-0",
		Workspace:  "ws-364",
		Team:       "squad",
		Role:       "worker",
		Generation: 0,
		State:      api.SessionRunning,
		PaneID:     "%7",
	}
	if err := store.CreateSession(sess); err != nil {
		t.Fatalf("create row: %v", err)
	}
	return store, mgr, ring, *sess
}

// assertRowKept checks the defect's consequence directly: after a failed
// kill the row is still in the store, so the role/generation listing that
// nextIndex (team.Controller) scans still holds index 0 and the next spawn
// cannot be handed the same name, home and checkout.
func assertRowKept(t *testing.T, store *api.Store, ring *events.Ring, sess api.Session, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("Delete returned nil after a failed kill; want an error naming the session")
	}
	if !strings.Contains(err.Error(), sess.Key()) {
		t.Errorf("Delete error %q does not name session %s", err, sess.Key())
	}
	got, gerr := store.GetSession(sess.Key())
	if gerr != nil {
		t.Fatalf("row %s dropped after a failed kill: %v", sess.Key(), gerr)
	}
	if got.State != api.SessionFailed {
		t.Errorf("kept row state = %s, want %s", got.State, api.SessionFailed)
	}
	if got.PaneID != sess.PaneID {
		t.Errorf("kept row PaneID = %q, want %q (a later reap needs it)", got.PaneID, sess.PaneID)
	}
	rows := store.ListSessionsByTeamRoleGeneration(sess.Workspace, sess.Team, sess.Role, sess.Generation)
	if len(rows) != 1 || rows[0].Name != sess.Name {
		t.Errorf("role/generation listing = %v, want the kept row so nextIndex skips index 0", rows)
	}
	if api.OccupiesReplicaSlot(got) {
		t.Errorf("kept kill-failed row occupies a replica slot; it must not count as a live replica")
	}
	kf := ring.Snapshot(events.Filter{Kind: "session.kill-failed"}, 0)
	if len(kf) != 1 {
		t.Errorf("session.kill-failed events = %d, want 1", len(kf))
	}
	if del := ring.Snapshot(events.Filter{Kind: events.KindSessionDeleted}, 0); len(del) != 0 {
		t.Errorf("session.deleted emitted for a row that was kept: %v", del)
	}
}

// TestDeleteKeepsRowWhenAdoptedPaneKillFails covers the driver branch: an
// adopted pane has no instance, so Delete kills it through the driver.
// ArcavenAE/marvel#364.
func TestDeleteKeepsRowWhenAdoptedPaneKillFails(t *testing.T) {
	store, mgr, ring, sess := keptRowFixture(t)
	mgr.killPane = func(string) error { return errKillRefused }

	err := mgr.Delete(sess.Key())
	assertRowKept(t, store, ring, sess, err)
}

// TestDeleteKeepsRowWhenInstanceKillFails covers the instance branch, the
// common case for sessions this daemon spawned: retireInstance used to log
// the kill error and report success.
func TestDeleteKeepsRowWhenInstanceKillFails(t *testing.T) {
	store, mgr, ring, sess := keptRowFixture(t)
	panes := &failingPanes{}
	inst := runtime.NewTmuxInstance(runtime.TmuxConfig{Panes: panes, TmuxSession: "ws-364", Title: sess.Name})
	if err := inst.Spawn(context.Background()); err != nil {
		t.Fatalf("spawn fake instance: %v", err)
	}
	mgr.imu.Lock()
	mgr.instances[sess.Key()] = inst
	mgr.imu.Unlock()
	mgr.killPane = func(string) error {
		t.Error("driver kill called for a session that has an instance")
		return nil
	}

	err := mgr.Delete(sess.Key())
	if len(panes.killed) != 1 {
		t.Fatalf("instance kill attempts = %d, want 1", len(panes.killed))
	}
	assertRowKept(t, store, ring, sess, err)
}

// TestDeleteRemovesRowWhenKillSucceeds is the unchanged happy path.
func TestDeleteRemovesRowWhenKillSucceeds(t *testing.T) {
	store, mgr, ring, sess := keptRowFixture(t)
	var killed []string
	mgr.killPane = func(id string) error { killed = append(killed, id); return nil }

	if err := mgr.Delete(sess.Key()); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(killed) != 1 || killed[0] != sess.PaneID {
		t.Errorf("killed = %v, want [%s]", killed, sess.PaneID)
	}
	if _, err := store.GetSession(sess.Key()); err == nil {
		t.Error("row still present after a successful kill")
	}
	if del := ring.Snapshot(events.Filter{Kind: events.KindSessionDeleted}, 0); len(del) != 1 {
		t.Errorf("session.deleted events = %d, want 1", len(del))
	}
}
