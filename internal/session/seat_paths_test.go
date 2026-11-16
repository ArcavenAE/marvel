package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/paths"
)

// A running seat keeps reading the settings file it was launched with, so a
// policy edit has to reach that path even after the projection directory has
// moved. The daemon records each seat's launch-time projection path and
// Reproject writes every recorded path as well as the current one
// (aae-orc-bc107, director ruling on the move of the default directories).

func policyEdited(t *testing.T, mgr *Manager) {
	t.Helper()
	edited := strings.Replace(projectionManifest, `allow = ["Read", "Grep"]`, `allow = ["Read", "Grep", "Edit"]`, 1)
	m, err := api.ParseManifestBytes([]byte(edited))
	if err != nil {
		t.Fatalf("parse edited manifest: %v", err)
	}
	if err := m.Apply(mgr.store); err != nil {
		t.Fatalf("apply edited manifest: %v", err)
	}
}

// recordPath stamps a launch-time projection path on the live session, as a
// seat launched before the directory moved would carry.
func recordPath(t *testing.T, mgr *Manager, path string) *api.Session {
	t.Helper()
	live := mgr.store.ListSessions()
	if len(live) != 1 {
		t.Fatalf("%d sessions, want 1", len(live))
	}
	if err := mgr.store.UpdateSession(live[0].Key(), func(s *api.Session) error {
		s.ProjectionPath = path
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := mgr.store.GetSession(live[0].Key())
	if err != nil {
		t.Fatal(err)
	}
	return &got
}

func TestLaunchRecordsTheProjectionPathItWasLaunchedWith(t *testing.T) {
	t.Parallel()
	mgr := projectionReady(t)
	sess := sessionFor("reviewer", "claude")

	mgr.planLaunch(sess)
	if sess.ProjectionPath == "" {
		t.Fatal("ProjectionPath is empty, want the path of the projected file this launch passes the harness")
	}
	if !withinDir(mgr.ProjectionDir, sess.ProjectionPath) {
		t.Errorf("ProjectionPath = %q, want a path inside %q", sess.ProjectionPath, mgr.ProjectionDir)
	}
	if _, err := os.Stat(sess.ProjectionPath); err != nil {
		t.Errorf("the recorded file is not on disk: %v", err)
	}
}

func TestReprojectWritesTheRecordedLaunchPathOfAnAdoptedSeat(t *testing.T) {
	t.Parallel()
	mgr := projectionReady(t)
	oldDir := t.TempDir()
	old := filepath.Join(oldDir, "seat.settings.json")
	sess := recordPath(t, mgr, old)

	mgr.Reproject()
	lctx, adapter, ok := mgr.reprojectContext(*sess)
	if !ok {
		t.Fatal("no launch context for the session")
	}
	current := adapter.ProjectionFor(lctx, mgr.ProjectionDir).Path
	got, err := os.ReadFile(old)
	if err != nil {
		t.Fatalf("the recorded launch path was not written: %v", err)
	}
	want, err := os.ReadFile(current)
	if err != nil {
		t.Fatalf("the current path was not written: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("recorded path holds %q, want the current projection %q", got, want)
	}

	policyEdited(t, mgr)
	mgr.Reproject()
	got, err = os.ReadFile(old)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"Edit"`) {
		t.Errorf("after a policy edit the recorded path holds %q, want the new rule", got)
	}
}

// A seat with no recorded path (launched before the field existed), and one
// whose recorded path is the current one, write the current path only.
func TestReprojectWritesTheCurrentPathOnceWhenNothingElseIsRecorded(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		record func(*Manager, *api.Session) string
	}{
		{"no recorded path", func(*Manager, *api.Session) string { return "" }},
		{"the recorded path is the current one", func(mgr *Manager, s *api.Session) string {
			lctx, adapter, _ := mgr.reprojectContext(*s)
			return adapter.ProjectionFor(lctx, mgr.ProjectionDir).Path
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mgr := projectionReady(t)
			live := mgr.store.ListSessions()
			recordPath(t, mgr, tc.record(mgr, &live[0]))
			if n := mgr.Reproject(); n != 1 {
				t.Errorf("Reproject changed = %d, want 1", n)
			}
			if n := entries(t, mgr.ProjectionDir); n != 1 {
				t.Errorf("the projection directory holds %d entries, want 1", n)
			}
		})
	}
}

// The private-directory rule holds for a recorded path too: nothing is written
// into a directory that is not private to this user, and the current path is
// still written.
func TestReprojectDoesNotWriteARecordedPathInADirThatIsNotPrivate(t *testing.T) {
	t.Parallel()
	mgr := projectionReady(t)
	oldDir := t.TempDir()
	widen(t, oldDir)
	recordPath(t, mgr, filepath.Join(oldDir, "seat.settings.json"))

	mgr.Reproject()
	if n := entries(t, oldDir); n != 0 {
		t.Errorf("the recorded directory holds %d entries, want 0: it is not private to this user", n)
	}
	if n := entries(t, mgr.ProjectionDir); n != 1 {
		t.Errorf("the projection directory holds %d entries, want 1", n)
	}
}

// The three defaults sit under the marvel home, which nothing sweeps, beside
// the harness homes, and not in the shared temp directory.
func TestDefaultSeatDirsAreUnderTheMarvelHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.HomeEnv, home)
	layout, err := paths.Default()
	if err != nil {
		t.Skipf("scratch home not usable here: %v", err)
	}
	for name, dir := range map[string]string{
		"projection": defaultProjectionDir(),
		"stream":     defaultStreamDir(),
		"overlay":    defaultBackendOverlayDir(),
	} {
		if !strings.HasPrefix(dir, layout.Home+string(os.PathSeparator)) {
			t.Errorf("%s directory = %q, want it under the marvel home %q", name, dir, layout.Home)
		}
	}
}

// The path recorded at launch reaches the stored session, not only the local
// copy the launch built, so it survives a daemon restart and Reproject can read
// it for an adopted seat.
func TestCreateStoresTheProjectionPathOfTheSeat(t *testing.T) {
	r := newContainerRig(t, "test-recorded-path", api.PlacementLocalhost, api.Role{Name: "seat", Replicas: 1, Policy: "contract"})
	r.mgr.ProjectionDir = t.TempDir()
	if err := r.store.CreatePolicy(&api.Policy{
		Name: "contract", Workspace: r.ws, Settings: map[string]any{"permissions": map[string]any{"allow": []any{"Read"}}},
	}); err != nil {
		t.Fatal(err)
	}
	sess := r.session("claude")
	if err := r.mgr.Create(sess); err != nil {
		t.Fatalf("Create: %v", err)
	}
	stored, err := r.store.GetSession(sess.Key())
	if err != nil {
		t.Fatal(err)
	}
	if stored.ProjectionPath == "" || !withinDir(r.mgr.ProjectionDir, stored.ProjectionPath) {
		t.Errorf("stored ProjectionPath = %q, want a path inside %q", stored.ProjectionPath, r.mgr.ProjectionDir)
	}
}
