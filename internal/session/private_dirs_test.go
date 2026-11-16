package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/runtime"
)

// The directories the daemon writes a seat's settings file, backend overlay and
// stream pipe into are held to the rule the token and harness-home paths
// already follow: a plain directory, mode 0700, owned by this user, checked
// before anything is written, and a launch is refused rather than downgraded
// when the check fails (aae-orc-bc107). Every directory below is under
// t.TempDir; nothing here reads or writes the shared temp directory.

// widen gives a scratch directory a mode wider than 0700, the shape a directory
// made by another local user would have.
func widen(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
}

func someoneElses(os.FileInfo) bool { return false }

// projectionReady is a projection manager with a policy applied and one live
// session of the role that references it, so Reproject has a file to write.
func projectionReady(t *testing.T) *Manager {
	t.Helper()
	mgr, _ := projectionManager(t)
	m, err := api.ParseManifestBytes([]byte(projectionManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := m.Apply(mgr.store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	sess := sessionFor("reviewer", "claude")
	if err := mgr.store.CreateSession(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := mgr.store.UpdateSession(sess.Key(), func(live *api.Session) error {
		live.State = api.SessionRunning
		live.PaneID = "%1"
		return nil
	}); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	return mgr
}

func TestProjectionIsNotWrittenInADirThatIsNotPrivate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, *Manager)
	}{
		{"a mode wider than 0700", func(t *testing.T, m *Manager) { widen(t, m.ProjectionDir) }},
		{"a directory of another user", func(_ *testing.T, m *Manager) { m.ownerCheck = someoneElses }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mgr := projectionReady(t)
			tc.setup(t, mgr)
			if n := mgr.Reproject(); n != 0 {
				t.Errorf("Reproject changed = %d, want 0: the projection directory is not private to this user", n)
			}
			if n := entries(t, mgr.ProjectionDir); n != 0 {
				t.Errorf("the projection directory holds %d entries, want 0: nothing is written before the check passes", n)
			}
		})
	}
}

func TestBackendOverlayIsNotWrittenInADirThatIsNotPrivate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, *Manager)
	}{
		{"a mode wider than 0700", func(t *testing.T, m *Manager) { widen(t, m.BackendOverlayDir) }},
		{"a directory of another user", func(_ *testing.T, m *Manager) { m.ownerCheck = someoneElses }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mgr := &Manager{BackendOverlayDir: t.TempDir()}
			tc.setup(t, mgr)
			sess := newBackendTestSession("bedrock", nil)
			plan := &launchPlan{env: map[string]string{}}

			if _, err := mgr.applyBackendOverlay(sess, plan); err == nil {
				t.Error("applyBackendOverlay succeeded, want a refusal: the overlay directory is not private to this user")
			}
			if n := entries(t, mgr.BackendOverlayDir); n != 0 {
				t.Errorf("the overlay directory holds %d entries, want 0: nothing is written before the check passes", n)
			}
			if sess.BackendOverlayPath != "" {
				t.Errorf("BackendOverlayPath = %q, want it unset on a refusal", sess.BackendOverlayPath)
			}
		})
	}
}

func TestStreamPipeIsNotMadeInADirThatIsNotPrivate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, *Manager)
	}{
		{"a mode wider than 0700", func(t *testing.T, m *Manager) { widen(t, m.StreamDir) }},
		{"a directory of another user", func(_ *testing.T, m *Manager) { m.ownerCheck = someoneElses }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mgr := &Manager{adapters: runtime.NewRegistry(), StreamDir: t.TempDir()}
			tc.setup(t, mgr)
			sess := sessionFor("worker", "claude")
			sess.Runtime.Mode = api.RuntimeModeHeadless
			lctx := &runtime.LaunchContext{Session: sess}

			fifo := mgr.openSink(sess, mgr.adapters.Resolve("claude"), lctx)
			defer discardSink(fifo)
			if fifo != nil {
				t.Error("openSink made a pipe, want none: the stream directory is not private to this user")
			}
			if n := entries(t, mgr.StreamDir); n != 0 {
				t.Errorf("the stream directory holds %d entries, want 0: nothing is made before the check passes", n)
			}
		})
	}
}

// A private directory of this user still works for all three. These guards pass
// before the change.
func TestPrivateDirsStillTakeAFileAndAPipe(t *testing.T) {
	t.Parallel()

	mgr := projectionReady(t)
	if n := mgr.Reproject(); n != 1 {
		t.Errorf("Reproject changed = %d, want 1 in a private projection directory", n)
	}

	om := &Manager{BackendOverlayDir: t.TempDir()}
	osess := newBackendTestSession("bedrock", nil)
	if _, err := om.applyBackendOverlay(osess, &launchPlan{env: map[string]string{}}); err != nil {
		t.Errorf("applyBackendOverlay in a private directory: %v", err)
	}
	if n := entries(t, om.BackendOverlayDir); n != 1 {
		t.Errorf("the overlay directory holds %d entries, want 1", n)
	}

	sm := &Manager{adapters: runtime.NewRegistry(), StreamDir: t.TempDir()}
	ssess := sessionFor("worker", "claude")
	ssess.Runtime.Mode = api.RuntimeModeHeadless
	fifo := sm.openSink(ssess, sm.adapters.Resolve("claude"), &runtime.LaunchContext{Session: ssess})
	defer discardSink(fifo)
	if fifo == nil {
		t.Error("openSink made no pipe in a private stream directory")
	}
}

// A settings file is written to a new name and renamed into place, so a link
// left at the target path is replaced and never followed: what the link pointed
// at keeps its bytes, and the path holds a plain file with the new content.
func TestProjectionWriteReplacesALinkAndNeverFollowsIt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim.json")
	if err := os.WriteFile(victim, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "squad-reviewer-g1-0.settings.json")
	if err := os.Symlink(victim, path); err != nil {
		t.Fatal(err)
	}

	if _, err := writeProjectionFile(path, map[string]any{"permissions": map[string]any{"allow": []string{"Read"}}}); err != nil {
		t.Fatalf("writeProjectionFile: %v", err)
	}

	if got, err := os.ReadFile(victim); err != nil || string(got) != "keep\n" {
		t.Errorf("the file the link pointed at = %q, %v; want it untouched", got, err)
	}
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("target path = %v, %v; want a plain file", fi, err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
}
