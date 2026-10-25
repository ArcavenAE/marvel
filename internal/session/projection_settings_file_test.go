package session

import (
	"errors"
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
	key := seedReviewerSession(t, mgr)
	return mgr, ring, key, filepath.Join(mgr.ProjectionDir, strings.ReplaceAll(key, "/", "-")+".settings.json")
}

// seedReviewerSession registers a running claude session of the reviewer role
// the manifests above declare, and returns its key.
func seedReviewerSession(t *testing.T, mgr *Manager) string {
	t.Helper()
	sess := &api.Session{
		Name:      "squad-reviewer-g1-0",
		Workspace: "acme",
		Team:      "squad",
		Role:      "reviewer",
		Runtime:   api.Runtime{Name: "claude", Command: "claude"},
	}
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
	return sess.Key()
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
	seedReviewerSession(t, mgr)
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

// A failure remembered for a session is dropped when the session is gone, so
// the memory does not grow with session churn.
func TestSettingsFileFailureMemoryIsPrunedForDeletedSessions(t *testing.T) {
	t.Parallel()
	path := writeSettingsFile(t, t.TempDir(), reviewerSettingsJSON)
	mgr, ring, key, _ := settingsFileSession(t, path)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	mgr.ReprojectFileBacked()
	if n := len(ring.Snapshot(events.Filter{Kind: events.KindPolicyProjectionFailed, Session: key}, 0)); n != 1 {
		t.Fatalf("policy.projection-failed events = %d, want 1", n)
	}
	if n := mgr.settingsFileFailureCount(); n != 1 {
		t.Fatalf("remembered failures = %d, want 1", n)
	}

	if err := mgr.store.DeleteSession(key); err != nil {
		t.Fatal(err)
	}
	mgr.ReprojectFileBacked()
	if n := mgr.settingsFileFailureCount(); n != 0 {
		t.Errorf("remembered failures after the session was deleted = %d, want 0", n)
	}
}

// settingsFileFailureCount is how many sessions have a failure remembered.
func (m *Manager) settingsFileFailureCount() int {
	m.failures.mu.Lock()
	defer m.failures.mu.Unlock()
	return len(m.failures.last)
}

// holdSession is the session a reconcile would spawn for the reviewer role.
func holdSession() *api.Session {
	return &api.Session{
		Name:      "squad-reviewer-g1-0",
		Workspace: "acme",
		Team:      "squad",
		Role:      "reviewer",
		Runtime:   api.Runtime{Name: "claude", Command: "claude"},
	}
}

// createExpectingHold calls Create and fails the test, rather than crashing it,
// when Create goes on to launch: these managers have no tmux driver, so a Create
// that is not held reaches it.
func createExpectingHold(t *testing.T, mgr *Manager, sess *api.Session) (err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Create went on to launch with an unreadable settings file: %v", r)
			err = nil
		}
	}()
	return mgr.Create(sess)
}

func applyForHold(t *testing.T, path string) *Manager {
	t.Helper()
	mgr, _ := projectionManager(t)
	m, err := api.ParseManifestBytes([]byte(settingsFileProjectionManifest(path)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := m.Apply(mgr.store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return mgr
}

// A spawn for a role whose settings file cannot be read is held: Create returns
// an error wrapping ErrSettingsFileRefused, no session record exists, and the
// event is emitted once however many ticks retry it.
func TestCreateHoldsASpawnWhoseSettingsFileCannotBeRead(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]*string{"missing": nil, "not an object": ptr(`["Read"]`), "not JSON": ptr("permissions: allow")} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "reviewer.json")
			if body != nil {
				writeSettingsFile(t, dir, *body)
			}
			mgr := applyForHold(t, path)
			ring := mgr.Events.(*events.Ring)

			for tick := 0; tick < 3; tick++ {
				err := createExpectingHold(t, mgr, holdSession())
				if !errors.Is(err, ErrSettingsFileRefused) {
					t.Fatalf("tick %d: Create error = %v, want one wrapping ErrSettingsFileRefused", tick, err)
				}
			}
			if n := len(mgr.store.ListSessions()); n != 0 {
				t.Errorf("a held spawn left %d session record(s), want 0", n)
			}
			failed := ring.Snapshot(events.Filter{Kind: events.KindPolicyProjectionFailed}, 0)
			if len(failed) != 1 {
				t.Fatalf("policy.projection-failed events over three ticks = %d, want 1", len(failed))
			}
			if failed[0].Severity != events.SeverityError || !strings.Contains(failed[0].Message, path) {
				t.Errorf("event = %+v, want error severity naming the file", failed[0])
			}
			if n := mgr.settingsFileFailureCount(); n != 1 {
				t.Errorf("remembered failures = %d, want 1", n)
			}
		})
	}
}

func ptr(s string) *string { return &s }

// Once the file reads, the spawn is no longer held and the remembered failure is
// dropped, so a later failure reports again.
func TestHeldSpawnIsAdmittedOnceTheSettingsFileReads(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "reviewer.json")
	mgr := applyForHold(t, path)
	ring := mgr.Events.(*events.Ring)

	if err := mgr.checkSettingsFile(holdSession()); !errors.Is(err, ErrSettingsFileRefused) {
		t.Fatalf("missing file: checkSettingsFile = %v, want ErrSettingsFileRefused", err)
	}
	writeSettingsFile(t, dir, reviewerSettingsJSON)
	if err := mgr.checkSettingsFile(holdSession()); err != nil {
		t.Fatalf("readable file: checkSettingsFile = %v, want nil", err)
	}
	if n := mgr.settingsFileFailureCount(); n != 0 {
		t.Errorf("remembered failures after the file read = %d, want 0", n)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := mgr.checkSettingsFile(holdSession()); !errors.Is(err, ErrSettingsFileRefused) {
		t.Fatalf("file removed again: checkSettingsFile = %v, want ErrSettingsFileRefused", err)
	}
	if n := len(ring.Snapshot(events.Filter{Kind: events.KindPolicyProjectionFailed}, 0)); n != 2 {
		t.Errorf("policy.projection-failed events across an outage, a recovery and a second outage = %d, want 2", n)
	}
}

// The hold covers only a policy that reads a file. An inline policy, a role with
// no policy and a session of no team are admitted.
func TestSettingsFileHoldLeavesOtherSpawnsAlone(t *testing.T) {
	t.Parallel()
	mgr, _ := projectionManager(t)
	m, err := api.ParseManifestBytes([]byte(projectionManifest))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(mgr.store); err != nil {
		t.Fatal(err)
	}
	if err := mgr.checkSettingsFile(holdSession()); err != nil {
		t.Errorf("inline policy: checkSettingsFile = %v, want nil", err)
	}
	adhoc := &api.Session{Name: "adhoc", Workspace: "other", Team: "none", Role: "none"}
	if err := mgr.checkSettingsFile(adhoc); err != nil {
		t.Errorf("session of no team: checkSettingsFile = %v, want nil", err)
	}
}

// planLaunch is the backstop for a file that goes bad between Create's check and
// the projection: it returns a plan carrying the hold, never a command line.
func TestPlanLaunchHoldsWhenTheSettingsFileCannotBeRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "reviewer.json")
	mgr := applyForHold(t, path)

	plan := mgr.planLaunch(holdSession())
	if plan.hold == nil {
		t.Fatalf("plan for an unreadable settings file = %+v, want hold set", plan)
	}
	if plan.command != "" {
		t.Errorf("held plan carries command %q, want none", plan.command)
	}
	if !strings.Contains(plan.hold.Error(), path) {
		t.Errorf("hold = %v, want it to name the file", plan.hold)
	}

	writeSettingsFile(t, dir, reviewerSettingsJSON)
	if plan := mgr.planLaunch(holdSession()); plan.hold != nil || plan.command == "" {
		t.Errorf("plan once the file reads = %+v, want a command and no hold", plan)
	}
}
