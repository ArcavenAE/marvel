package session

import (
	"fmt"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// The reconcile pass calls ReprojectFileBacked every tick. When no policy reads
// a settings file there is nothing for it to do, so it should not visit, clone
// or look up anything per live session.

func seedAliveSessions(t testing.TB, mgr *Manager, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		sess := &api.Session{
			Name: fmt.Sprintf("squad-reviewer-g1-%d", i), Workspace: "acme", Team: "squad", Role: "reviewer",
			Runtime: api.Runtime{Name: "claude", Command: "claude"},
		}
		if err := mgr.store.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
		if err := mgr.store.UpdateSession(sess.Key(), func(live *api.Session) error {
			live.State = api.SessionRunning
			live.PaneID = "%1"
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func inlineFleet(t *testing.T, sessions int) *Manager {
	t.Helper()
	mgr, _ := projectionManager(t)
	m, err := api.ParseManifestBytes([]byte(projectionManifest))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(mgr.store); err != nil {
		t.Fatal(err)
	}
	seedAliveSessions(t, mgr, sessions)
	return mgr
}

func TestReprojectFileBackedAllocatesNothingPerSessionForInlinePolicies(t *testing.T) {
	const sessions = 30
	mgr := inlineFleet(t, sessions)
	mgr.ReprojectFileBacked() // warm
	allocs := testing.AllocsPerRun(20, func() { mgr.ReprojectFileBacked() })
	t.Logf("ReprojectFileBacked, %d seats, inline policy only: %.0f allocations", sessions, allocs)
	const bound = 8
	if allocs > bound {
		t.Errorf("ReprojectFileBacked allocated %.0f times with no file-backed policy, want at most %d", allocs, bound)
	}
}
