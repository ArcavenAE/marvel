package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/runtime"
)

const projectionManifest = `
[workspace]
name = "acme"

[[policy]]
name = "reviewer-contract"
version = "1.0"

  [policy.settings.permissions]
  allow = ["Read", "Grep"]

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

// projectionManager builds a Manager wired only for projection: a store, a
// tempdir projection root, an event ring, and no tmux driver. Reproject and
// the projection helpers never touch the driver, so nil is safe here and
// keeps the test hermetic (no tmux server required).
func projectionManager(t *testing.T) (*Manager, *events.Ring) {
	t.Helper()
	store := api.NewStore()
	ring := events.NewRing(64)
	mgr := &Manager{
		store:         store,
		adapters:      runtime.NewRegistry(),
		ProjectionDir: t.TempDir(),
		Events:        ring,
	}
	return mgr, ring
}

// seedRunningSession registers a Running session for the given role so
// Reproject treats it as live. Returns the session key.
func seedRunningSession(t *testing.T, mgr *Manager, workspace, team, role, name, image string) string {
	t.Helper()
	sess := &api.Session{
		Name:      name,
		Workspace: workspace,
		Team:      team,
		Role:      role,
		Runtime:   api.Runtime{Name: image, Command: image},
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

func readProjection(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read projection %s: %v", path, err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal projection: %v", err)
	}
	return out
}

func TestReprojectRewritesOnPolicyChange(t *testing.T) {
	t.Parallel()
	mgr, ring := projectionManager(t)

	m, err := api.ParseManifestBytes([]byte(projectionManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := m.Apply(mgr.store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	key := seedRunningSession(t, mgr, "acme", "squad", "reviewer", "squad-reviewer-g1-0", "claude")

	// First projection: fresh file, counts as a change.
	if n := mgr.Reproject(); n != 1 {
		t.Fatalf("first Reproject changed = %d, want 1", n)
	}
	path := filepath.Join(mgr.ProjectionDir, strings.ReplaceAll(key, "/", "-")+".settings.json")
	got := readProjection(t, path)
	perms, ok := got["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("projection missing permissions block: %v", got)
	}
	if allow, ok := perms["allow"].([]any); !ok || len(allow) != 2 {
		t.Fatalf("permissions.allow = %v, want two entries", perms["allow"])
	}

	// Re-projecting with no change writes nothing new.
	if n := mgr.Reproject(); n != 0 {
		t.Fatalf("no-op Reproject changed = %d, want 0", n)
	}

	// Edit the policy: a second version tightening the allow list.
	edited := strings.Replace(projectionManifest,
		`allow = ["Read", "Grep"]`, `allow = ["Read"]`, 1)
	m2, err := api.ParseManifestBytes([]byte(edited))
	if err != nil {
		t.Fatalf("parse edited: %v", err)
	}
	if err := m2.Apply(mgr.store); err != nil {
		t.Fatalf("apply edited: %v", err)
	}

	// Re-projection after the edit rewrites the running session's file.
	if n := mgr.Reproject(); n != 1 {
		t.Fatalf("post-edit Reproject changed = %d, want 1", n)
	}
	got2 := readProjection(t, path)
	perms2 := got2["permissions"].(map[string]any)
	if allow, ok := perms2["allow"].([]any); !ok || len(allow) != 1 {
		t.Fatalf("post-edit permissions.allow = %v, want one entry", perms2["allow"])
	}

	// Both changes emitted an observable event.
	evs := ring.Snapshot(events.Filter{Kind: events.KindPolicyProjected, Session: key}, 0)
	if len(evs) != 2 {
		t.Fatalf("policy.projected events = %d, want 2", len(evs))
	}
	if !strings.Contains(evs[1].Message, "re-projected") {
		t.Errorf("second event message = %q, want re-projection detail", evs[1].Message)
	}
}

func TestReprojectSkipsUnsupportedRuntime(t *testing.T) {
	t.Parallel()
	mgr, ring := projectionManager(t)

	// A codex role referencing a policy: codex has no settings surface, so
	// the policy is advisory — nothing written, nothing counted, but the
	// session is otherwise valid.
	manifest := strings.Replace(projectionManifest,
		`    image = "claude"
    command = "claude"`,
		`    image = "codex"
    command = "codex"`, 1)
	m, err := api.ParseManifestBytes([]byte(manifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := m.Apply(mgr.store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	key := seedRunningSession(t, mgr, "acme", "squad", "reviewer", "squad-reviewer-g1-0", "codex")

	if n := mgr.Reproject(); n != 0 {
		t.Fatalf("Reproject changed = %d, want 0 for advisory policy", n)
	}
	path := filepath.Join(mgr.ProjectionDir, strings.ReplaceAll(key, "/", "-")+".settings.json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("advisory policy wrote a file at %s (err=%v)", path, err)
	}
	if n := len(ring.Snapshot(events.Filter{Kind: events.KindPolicyProjected}, 0)); n != 0 {
		t.Errorf("advisory policy emitted %d projection events, want 0", n)
	}
}

func TestReprojectNoPolicyIsNoOp(t *testing.T) {
	t.Parallel()
	mgr, _ := projectionManager(t)

	// Same manifest without the role policy reference.
	manifest := strings.Replace(projectionManifest,
		"  policy = \"reviewer-contract\"\n", "", 1)
	m, err := api.ParseManifestBytes([]byte(manifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := m.Apply(mgr.store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	seedRunningSession(t, mgr, "acme", "squad", "reviewer", "squad-reviewer-g1-0", "claude")

	if n := mgr.Reproject(); n != 0 {
		t.Fatalf("Reproject changed = %d, want 0 with no policy reference", n)
	}
}

const feedOnlyManifest = `
[workspace]
name = "acme"

[[team]]
name = "squad"

  [[team.role]]
  name = "watcher"
  replicas = 1

    [team.role.runtime]
    image = "claude"
    command = "claude"
    context_feed = "statusline"
`

const feedWithPolicyManifest = `
[workspace]
name = "acme"

[[policy]]
name = "own-statusline"
version = "1.0"

  [policy.settings.statusLine]
  type = "command"
  command = "/usr/local/bin/my-statusline"

[[team]]
name = "squad"

  [[team.role]]
  name = "watcher"
  replicas = 1
  policy = "own-statusline"

    [team.role.runtime]
    image = "claude"
    command = "claude"
    context_feed = "statusline"
`

// seedFeedSession is seedRunningSession with ContextFeed set on the
// session's runtime, the way reconcileRole copies it from the role.
func seedFeedSession(t *testing.T, mgr *Manager, workspace, team, role, name string) string {
	t.Helper()
	sess := &api.Session{
		Name:      name,
		Workspace: workspace,
		Team:      team,
		Role:      role,
		Runtime:   api.Runtime{Name: "claude", Command: "claude", ContextFeed: api.ContextFeedStatusline},
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

// TestProjectionInjectsStatuslineFeed covers finding-011: a role with
// context_feed = "statusline" and NO policy still gets a projected
// settings file carrying the ctx-forward hooks. Falsification: with the
// old policy-only gate in projectPolicy, no file is written at all.
func TestProjectionInjectsStatuslineFeed(t *testing.T) {
	t.Parallel()
	mgr, _ := projectionManager(t)

	m, err := api.ParseManifestBytes([]byte(feedOnlyManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := m.Apply(mgr.store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	key := seedFeedSession(t, mgr, "acme", "squad", "watcher", "squad-watcher-g1-0")

	if n := mgr.Reproject(); n != 1 {
		t.Fatalf("Reproject changed = %d, want 1", n)
	}
	path := filepath.Join(mgr.ProjectionDir, strings.ReplaceAll(key, "/", "-")+".settings.json")
	got := readProjection(t, path)

	sl, ok := got["statusLine"].(map[string]any)
	if !ok {
		t.Fatalf("projection missing statusLine: %v", got)
	}
	cmd, _ := sl["command"].(string)
	if !strings.HasSuffix(cmd, " ctx-forward") {
		t.Errorf("statusLine.command = %q, want ctx-forward suffix", cmd)
	}
	if ri, ok := sl["refreshInterval"].(float64); !ok || ri != 15 {
		t.Errorf("statusLine.refreshInterval = %v, want 15", sl["refreshInterval"])
	}
	sub, ok := got["subagentStatusLine"].(map[string]any)
	if !ok {
		t.Fatalf("projection missing subagentStatusLine: %v", got)
	}
	if cmd, _ := sub["command"].(string); !strings.HasSuffix(cmd, " ctx-forward") {
		t.Errorf("subagentStatusLine.command = %q, want ctx-forward suffix", cmd)
	}
}

// feedlessAdapter reads a projected settings file but has no context-feed
// schema of its own. It stands in for the next harness added to marvel:
// something that accepts a settings path but is not Claude Code.
type feedlessAdapter struct{ dir string }

func (feedlessAdapter) Name() string { return "feedless" }

func (feedlessAdapter) Prepare(_ *runtime.LaunchContext) (*runtime.LaunchResult, error) {
	return &runtime.LaunchResult{Command: "feedless"}, nil
}

func (a feedlessAdapter) ProjectionFor(ctx *runtime.LaunchContext, dir string) runtime.ProjectionTarget {
	target := dir
	if a.dir != "" {
		target = a.dir
	}
	return runtime.ProjectionTarget{
		Supported: true,
		Path:      filepath.Join(target, strings.ReplaceAll(ctx.Session.Key(), "/", "-")+".settings.json"),
	}
}

// seedFeedSessionForRuntime is seedFeedSession with the runtime name under
// test, so the manager resolves the adapter registered for it.
func seedFeedSessionForRuntime(t *testing.T, mgr *Manager, image string) string {
	t.Helper()
	sess := &api.Session{
		Name:      "squad-watcher-g1-0",
		Workspace: "acme",
		Team:      "squad",
		Role:      "watcher",
		Runtime:   api.Runtime{Name: image, Command: image, ContextFeed: api.ContextFeedStatusline},
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

// applyFeedManifestForRuntime applies feedOnlyManifest with the claude
// runtime swapped for the named one.
func applyFeedManifestForRuntime(t *testing.T, mgr *Manager, image string) {
	t.Helper()
	manifest := strings.Replace(feedOnlyManifest,
		`    image = "claude"
    command = "claude"`,
		`    image = "`+image+`"
    command = "`+image+`"`, 1)
	m, err := api.ParseManifestBytes([]byte(manifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := m.Apply(mgr.store); err != nil {
		t.Fatalf("apply: %v", err)
	}
}

// TestProjectionFeedIsAdapterOwned covers marvel#146: the feed keys come
// from the adapter, so a harness that reads a projected settings file but
// declares no feed schema gets none. Falsification: with the undispatched
// call, Claude Code's statusLine and subagentStatusLine are written into
// this harness's file, where they mean nothing.
func TestProjectionFeedIsAdapterOwned(t *testing.T) {
	t.Parallel()
	mgr, _ := projectionManager(t)
	mgr.adapters.Register(feedlessAdapter{})

	applyFeedManifestForRuntime(t, mgr, "feedless")
	key := seedFeedSessionForRuntime(t, mgr, "feedless")

	// The file is still written: the projection surface is real, only the
	// feed schema is missing.
	if n := mgr.Reproject(); n != 1 {
		t.Fatalf("Reproject changed = %d, want 1", n)
	}
	path := filepath.Join(mgr.ProjectionDir, strings.ReplaceAll(key, "/", "-")+".settings.json")
	got := readProjection(t, path)

	for _, key := range []string{"statusLine", "subagentStatusLine"} {
		if v, ok := got[key]; ok {
			t.Errorf("projection carries Claude Code's %s for a non-Claude harness: %v", key, v)
		}
	}
}

// TestProjectionRefusesPathOutsideProjectionDir covers the second half of
// marvel#146's candidate adapter-contract rule: no adapter writes outside
// the directory it was handed. Falsification: without the guard, marvel
// creates and edits a settings file in a directory it does not own.
func TestProjectionRefusesPathOutsideProjectionDir(t *testing.T) {
	t.Parallel()
	mgr, ring := projectionManager(t)
	// An adapter pointing at the user's own config directory rather than
	// the projection dir, which is the shape the rule exists to refuse.
	userConfig := t.TempDir()
	mgr.adapters.Register(feedlessAdapter{dir: userConfig})

	applyFeedManifestForRuntime(t, mgr, "feedless")
	key := seedFeedSessionForRuntime(t, mgr, "feedless")

	if n := mgr.Reproject(); n != 0 {
		t.Fatalf("Reproject changed = %d, want 0 for an out-of-dir path", n)
	}
	stray := filepath.Join(userConfig, strings.ReplaceAll(key, "/", "-")+".settings.json")
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Errorf("wrote outside the projection dir at %s (err=%v)", stray, err)
	}
	if n := len(ring.Snapshot(events.Filter{Kind: events.KindPolicyProjected}, 0)); n != 0 {
		t.Errorf("refused projection emitted %d events, want 0", n)
	}
}

// TestDaemonTempDirsAreLayoutScoped covers marvel#147. The stream and
// projection directories have to be unique per daemon AND stable across a
// restart, because `marvel stop` leaves agents running and an adopted agent
// keeps reading the settings path it was launched with. Falsification: keyed
// on the process id, two HOMEs in one process share a directory (no
// isolation), which is what this asserts against.

// TestProjectionPolicyWinsOverFeed covers the merge contract: a policy
// that declares its own statusLine keeps it verbatim; the feed only adds
// keys the policy does not define. Falsification: if injection
// overwrites, the projected command is marvel's instead of the policy's.
func TestProjectionPolicyWinsOverFeed(t *testing.T) {
	t.Parallel()
	mgr, _ := projectionManager(t)

	m, err := api.ParseManifestBytes([]byte(feedWithPolicyManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := m.Apply(mgr.store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	key := seedFeedSession(t, mgr, "acme", "squad", "watcher", "squad-watcher-g1-0")

	if n := mgr.Reproject(); n != 1 {
		t.Fatalf("Reproject changed = %d, want 1", n)
	}
	path := filepath.Join(mgr.ProjectionDir, strings.ReplaceAll(key, "/", "-")+".settings.json")
	got := readProjection(t, path)

	sl := got["statusLine"].(map[string]any)
	if cmd, _ := sl["command"].(string); cmd != "/usr/local/bin/my-statusline" {
		t.Errorf("statusLine.command = %q, want the policy's own command (policy wins)", cmd)
	}
	if _, ok := got["subagentStatusLine"]; !ok {
		t.Error("subagentStatusLine absent: feed should add keys the policy does not define")
	}
}

// TestProjectionReportsShadowedFeedKey covers aae-orc-g698: when a policy
// declares a key the context feed also wants, policy-wins is correct AND the
// shadowing must be reported, so an operator can tell a working feed from one
// that populates only on subagent turns. Falsification: before the fix the
// merge is silent — the policy.projected event names the path and policy but
// says nothing about the feed key the policy shadowed, so this assertion
// fails. Deliberately separate from TestProjectionPolicyWinsOverFeed, which
// asserts the half-and-half merge state and must keep passing either way.
func TestProjectionReportsShadowedFeedKey(t *testing.T) {
	t.Parallel()
	mgr, ring := projectionManager(t)

	m, err := api.ParseManifestBytes([]byte(feedWithPolicyManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := m.Apply(mgr.store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	key := seedFeedSession(t, mgr, "acme", "squad", "watcher", "squad-watcher-g1-0")

	if n := mgr.Reproject(); n != 1 {
		t.Fatalf("Reproject changed = %d, want 1", n)
	}

	evs := ring.Snapshot(events.Filter{Kind: events.KindPolicyProjected, Session: key}, 0)
	if len(evs) != 1 {
		t.Fatalf("policy.projected events = %d, want 1", len(evs))
	}
	msg := evs[0].Message
	// "statusLine" (capital L) is the shadowed feed key. The policy name
	// "own-statusline" is all lowercase, so this substring matches the key
	// the report names, not the policy.
	if !strings.Contains(msg, "statusLine") {
		t.Errorf("event message %q does not name the shadowed feed key statusLine", msg)
	}
	if !strings.Contains(strings.ToLower(msg), "shadow") {
		t.Errorf("event message %q does not report the feed key as shadowed by the policy", msg)
	}
	// subagentStatusLine was NOT declared by the policy, so it is fed, not
	// shadowed; the report must not name it.
	if strings.Contains(msg, "subagentStatusLine") {
		t.Errorf("event message %q names subagentStatusLine, which the policy did not shadow", msg)
	}
}
