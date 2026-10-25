package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// A policy's settings_file is read verbatim at projection, re-read on each
// reconcile, and a file that cannot be read is an error event for the session,
// never an empty policy.

func settingsFileProjectionManifest(path string) string {
	return `
[workspace]
name = "acme"

[[policy]]
name = "reviewer-contract"
settings_file = "` + path + `"

[[team]]
name = "squad"

  [[team.role]]
  name = "reviewer"
  replicas = 1
  policy = "reviewer-contract"

    [team.role.runtime]
    image = "claude"
    command = "claude"
`
}

const reviewerSettingsJSON = `{
  "permissions": {"allow": ["Read", "Grep"], "deny": ["Bash(rm:*)"]},
  "autoMode": {"allow": ["Read the repository"]},
  "bigNumber": 12345678901234567890
}`

// settingsFileSession applies a manifest naming path, seeds one running
// session, and returns the manager, its event ring, the session key and the
// projected file's path.
func settingsFileSession(t *testing.T, path string) (*Manager, *events.Ring, string, string) {
	t.Helper()
	mgr, ring := projectionManager(t)
	m, err := api.ParseManifestBytes([]byte(settingsFileProjectionManifest(path)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := m.Apply(mgr.store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	key := seedRunningSession(t, mgr, "acme", "squad", "reviewer", "squad-reviewer-g1-0", "claude")
	return mgr, ring, key, filepath.Join(mgr.ProjectionDir, strings.ReplaceAll(key, "/", "-")+".settings.json")
}

func writeSettingsFile(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "reviewer.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProjectionReadsTheSettingsFileVerbatim(t *testing.T) {
	t.Parallel()
	path := writeSettingsFile(t, t.TempDir(), reviewerSettingsJSON)
	mgr, _, _, projected := settingsFileSession(t, path)

	if n := mgr.Reproject(); n != 1 {
		t.Fatalf("Reproject changed = %d, want 1", n)
	}
	got := readProjection(t, projected)
	perms, ok := got["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("projection has no permissions block: %v", got)
	}
	if allow, _ := perms["allow"].([]any); len(allow) != 2 {
		t.Errorf("permissions.allow = %v, want the file's two entries", perms["allow"])
	}
	if _, ok := got["autoMode"].(map[string]any); !ok {
		t.Errorf("projection has no autoMode block: %v", got)
	}
	raw, err := os.ReadFile(projected)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "12345678901234567890") {
		t.Errorf("a number in the file was not carried verbatim:\n%s", raw)
	}
}

func TestProjectionFileKeepsPolicyWinsOverFeed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeSettingsFile(t, dir, `{"statusLine": {"type": "command", "command": "/usr/local/bin/my-statusline"}}`)
	mgr, _, key, projected := settingsFileSession(t, path)
	// The same role with the statusline feed on.
	if err := mgr.store.UpdateSession(key, func(live *api.Session) error {
		live.Runtime.ContextFeed = api.ContextFeedStatusline
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n := mgr.Reproject(); n != 1 {
		t.Fatalf("Reproject changed = %d, want 1", n)
	}
	got := readProjection(t, projected)
	sl, _ := got["statusLine"].(map[string]any)
	if cmd, _ := sl["command"].(string); cmd != "/usr/local/bin/my-statusline" {
		t.Errorf("statusLine.command = %q, want the file's own command (policy wins)", cmd)
	}
	if _, ok := got["subagentStatusLine"]; !ok {
		t.Error("subagentStatusLine absent: the feed should add keys the file does not define")
	}
}

func TestChangedSettingsFileIsReprojectedOnTheNextReconcile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeSettingsFile(t, dir, reviewerSettingsJSON)
	mgr, ring, key, projected := settingsFileSession(t, path)
	if n := mgr.ReprojectFileBacked(); n != 1 {
		t.Fatalf("first ReprojectFileBacked changed = %d, want 1", n)
	}
	if n := mgr.ReprojectFileBacked(); n != 0 {
		t.Fatalf("unchanged file: ReprojectFileBacked changed = %d, want 0 (write only on change)", n)
	}

	writeSettingsFile(t, dir, `{"permissions": {"allow": ["Read"]}}`)
	if n := mgr.ReprojectFileBacked(); n != 1 {
		t.Fatalf("rewritten file: ReprojectFileBacked changed = %d, want 1", n)
	}
	got := readProjection(t, projected)
	perms, _ := got["permissions"].(map[string]any)
	if allow, _ := perms["allow"].([]any); len(allow) != 1 {
		t.Errorf("permissions.allow = %v, want the rewritten file's one entry", perms["allow"])
	}
	if _, stale := got["autoMode"]; stale {
		t.Error("autoMode survived a rewrite that dropped it")
	}
	evs := ring.Snapshot(events.Filter{Kind: events.KindPolicyProjected, Session: key}, 0)
	if len(evs) != 2 {
		t.Fatalf("policy.projected events = %d, want 2 (first projection, then the change)", len(evs))
	}
	if !strings.Contains(evs[1].Message, "settings file") {
		t.Errorf("second event message = %q, want it to say the settings file changed", evs[1].Message)
	}
}

// ReprojectFileBacked leaves a session whose policy carries inline settings alone.
func TestReprojectFileBackedSkipsInlinePolicies(t *testing.T) {
	t.Parallel()
	mgr, _ := projectionManager(t)
	m, err := api.ParseManifestBytes([]byte(projectionManifest))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(mgr.store); err != nil {
		t.Fatal(err)
	}
	seedRunningSession(t, mgr, "acme", "squad", "reviewer", "squad-reviewer-g1-0", "claude")
	if n := mgr.ReprojectFileBacked(); n != 0 {
		t.Errorf("ReprojectFileBacked changed = %d for an inline policy, want 0", n)
	}
}

func TestMissingSettingsFileIsAnErrorEventNotAnEmptyPolicy(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeSettingsFile(t, dir, reviewerSettingsJSON)
	mgr, ring, key, projected := settingsFileSession(t, path)
	if n := mgr.Reproject(); n != 1 {
		t.Fatalf("Reproject changed = %d, want 1", n)
	}
	before, err := os.ReadFile(projected)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if n := mgr.ReprojectFileBacked(); n != 0 {
		t.Errorf("missing file: ReprojectFileBacked changed = %d, want 0", n)
	}
	after, err := os.ReadFile(projected)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("a missing settings file replaced the projection:\n%s", after)
	}
	failed := ring.Snapshot(events.Filter{Kind: events.KindPolicyProjectionFailed, Session: key}, 0)
	if len(failed) != 1 {
		t.Fatalf("policy.projection-failed events = %d, want 1", len(failed))
	}
	if failed[0].Severity != events.SeverityError {
		t.Errorf("severity = %q, want error", failed[0].Severity)
	}
	for _, want := range []string{"reviewer-contract", path} {
		if !strings.Contains(failed[0].Message, want) {
			t.Errorf("message = %q, want it to mention %q", failed[0].Message, want)
		}
	}

	// The same failure on the next ticks is not reported again.
	mgr.ReprojectFileBacked()
	mgr.ReprojectFileBacked()
	if n := len(ring.Snapshot(events.Filter{Kind: events.KindPolicyProjectionFailed, Session: key}, 0)); n != 1 {
		t.Errorf("policy.projection-failed events after repeats = %d, want 1", n)
	}

	// When the file returns it projects again, and a later failure reports again.
	writeSettingsFile(t, dir, `{"permissions": {"allow": ["Read"]}}`)
	if n := mgr.ReprojectFileBacked(); n != 1 {
		t.Errorf("restored file: ReprojectFileBacked changed = %d, want 1", n)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	mgr.ReprojectFileBacked()
	if n := len(ring.Snapshot(events.Filter{Kind: events.KindPolicyProjectionFailed, Session: key}, 0)); n != 2 {
		t.Errorf("policy.projection-failed events after a second outage = %d, want 2", n)
	}
}

func TestUnreadableSettingsFilesAreErrorEvents(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"not JSON":          "permissions: allow",
		"a JSON array":      `["Read"]`,
		"a JSON string":     `"Read"`,
		"trailing garbage":  `{"permissions": {}} trailing`,
		"an empty file":     "",
		"two JSON objects":  `{"a": 1}{"b": 2}`,
		"a truncated write": `{"permissions": {"allow": ["Re`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := writeSettingsFile(t, t.TempDir(), body)
			mgr, ring, key, projected := settingsFileSession(t, path)
			mgr.Reproject()
			if _, err := os.Stat(projected); err == nil {
				t.Errorf("a settings file that is %s was projected", name)
			}
			if n := len(ring.Snapshot(events.Filter{Kind: events.KindPolicyProjectionFailed, Session: key}, 0)); n != 1 {
				t.Errorf("policy.projection-failed events = %d, want 1", n)
			}
			if n := len(ring.Snapshot(events.Filter{Kind: events.KindPolicyProjected, Session: key}, 0)); n != 0 {
				t.Errorf("policy.projected events = %d, want 0", n)
			}
		})
	}
}

func TestSettingsFileTildeIsExpandedToTheHome(t *testing.T) {
	// Not parallel: t.Setenv.
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeSettingsFile(t, home, `{"permissions": {"allow": ["Read"]}}`)
	mgr, _, _, projected := settingsFileSession(t, "~/reviewer.json")
	if n := mgr.Reproject(); n != 1 {
		t.Fatalf("Reproject changed = %d, want 1", n)
	}
	if got := readProjection(t, projected); got["permissions"] == nil {
		t.Errorf("projection has no permissions from ~/reviewer.json: %v", got)
	}
}
