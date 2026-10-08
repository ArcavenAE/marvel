package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/runtime"
	"github.com/arcavenae/marvel/internal/tmux"
)

const sessionIDManifest = `
[workspace]
name = "acme"

[[team]]
name = "squad"

  [[team.role]]
  name = "reviewer"
  replicas = 1

    [team.role.runtime]
    image = "claude"
    command = "claude"

  [[team.role]]
  name = "coder"
  replicas = 1

    [team.role.runtime]
    image = "codex"
    command = "codex"
`

// sessionIDManager builds a Manager wired for planLaunch only: a store and
// the adapter registry. planLaunch never touches the tmux driver, so nil is
// safe and the test stays hermetic.
func sessionIDManager(t *testing.T) *Manager {
	t.Helper()
	mgr := &Manager{
		store:         api.NewStore(),
		adapters:      runtime.NewRegistry(),
		ProjectionDir: t.TempDir(),
		Events:        events.NewRing(16),
	}
	m, err := api.ParseManifestBytes([]byte(sessionIDManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := m.Apply(mgr.store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return mgr
}

func sessionFor(role, image string) *api.Session {
	return &api.Session{
		Name:      "squad-" + role + "-g1-0",
		Workspace: "acme",
		Team:      "squad",
		Role:      role,
		Runtime:   api.Runtime{Name: image, Command: image},
	}
}

// The whole point of aae-orc-ca7y: the id the harness is told is the id
// marvel kept, so the pane-to-transcript binding needs no discovery.
func TestPlanLaunchRecordsTheIDItPutOnTheCommandLine(t *testing.T) {
	t.Parallel()
	mgr := sessionIDManager(t)
	sess := sessionFor("reviewer", "claude")

	plan := mgr.planLaunch(sess)

	if sess.HarnessSessionID == "" {
		t.Fatal("a claude launch should have been assigned a session id")
	}
	if !strings.Contains(plan.command, "--session-id "+sess.HarnessSessionID) {
		t.Errorf("command should carry the recorded id %q, got: %s",
			sess.HarnessSessionID, plan.command)
	}
}

// A restart must not replay the previous id: the harness exits 1 with
// "Session ID <uuid> is already in use", so a value held across respawns
// would fail every launch after the first.
func TestPlanLaunchMintsAFreshIDPerLaunch(t *testing.T) {
	t.Parallel()
	mgr := sessionIDManager(t)
	sess := sessionFor("reviewer", "claude")

	mgr.planLaunch(sess)
	first := sess.HarnessSessionID

	mgr.planLaunch(sess)
	second := sess.HarnessSessionID

	if first == "" || second == "" {
		t.Fatalf("both launches should be named, got %q then %q", first, second)
	}
	if first == second {
		t.Errorf("a relaunch reused id %q; the harness refuses that", first)
	}
}

// A runtime with no id pin must come away with nothing recorded, rather
// than an id nothing was ever told.
func TestPlanLaunchLeavesUnpinnableRuntimesUnnamed(t *testing.T) {
	t.Parallel()
	mgr := sessionIDManager(t)
	sess := sessionFor("coder", "codex")

	plan := mgr.planLaunch(sess)

	if sess.HarnessSessionID != "" {
		t.Errorf("codex has no id pin, so nothing should be recorded, got %q",
			sess.HarnessSessionID)
	}
	if strings.Contains(plan.command, "--session-id") {
		t.Errorf("codex command should carry no --session-id, got: %s", plan.command)
	}
}

// The sources passed to the harness are kept on the session, so describe and a
// restart can say what the seat was told (SB-2). A harness marvel passed none
// to records none.
func TestPlanLaunchRecordsTheSettingSources(t *testing.T) {
	t.Parallel()
	mgr := sessionIDManager(t)

	claude := sessionFor("reviewer", "claude")
	plan := mgr.planLaunch(claude)
	if !strings.Contains(plan.command, "--setting-sources user,project,local") {
		t.Fatalf("command %q, want --setting-sources user,project,local", plan.command)
	}
	if claude.SettingSources != "user,project,local" {
		t.Errorf("claude SettingSources = %q, want user,project,local", claude.SettingSources)
	}

	codex := sessionFor("coder", "codex")
	mgr.planLaunch(codex)
	if codex.SettingSources != "" {
		t.Errorf("codex SettingSources = %q, want empty", codex.SettingSources)
	}
}

// The delivery is recorded beside the sources, and a re-plan that passes
// nothing does not carry the previous launch's value forward (marvel#748).
func TestPlanLaunchRecordsTheSettingSourcesDelivery(t *testing.T) {
	t.Parallel()
	mgr := sessionIDManager(t)

	claude := sessionFor("reviewer", "claude")
	mgr.planLaunch(claude)
	if claude.SettingSourcesDelivery != api.SettingSourcesArgv {
		t.Errorf("claude delivery = %q, want %q", claude.SettingSourcesDelivery, api.SettingSourcesArgv)
	}

	codex := sessionFor("coder", "codex")
	codex.SettingSources = "user"
	codex.SettingSourcesDelivery = api.SettingSourcesShellText
	mgr.planLaunch(codex)
	if codex.SettingSources != "" || codex.SettingSourcesDelivery != "" {
		t.Errorf("codex kept the previous launch: sources %q delivery %q", codex.SettingSources, codex.SettingSourcesDelivery)
	}
}

// A launch that falls back to the direct command passes no sources, so the
// record of the previous launch is cleared with its delivery (marvel#748).
func TestDirectFallbackClearsTheSettingSourcesAndTheirDelivery(t *testing.T) {
	t.Parallel()
	mgr := sessionIDManager(t)
	sess := sessionFor("reviewer", "claude")
	sess.Team = "no-such-team"
	sess.SettingSources = "project"
	sess.SettingSourcesDelivery = api.SettingSourcesShellText
	mgr.planLaunch(sess)
	if sess.SettingSources != "" || sess.SettingSourcesDelivery != "" {
		t.Errorf("a direct launch kept sources %q delivery %q", sess.SettingSources, sess.SettingSourcesDelivery)
	}
}

// The live record keeps the delivery with the sources once the pane is up, so
// describe and a daemon restart read what the launch recorded (marvel#748).
func TestCreateStoresTheSettingSourcesDelivery(t *testing.T) {
	skipIfNoTmux(t)
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexec sleep 300\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	mgr := NewManager(store, driver)
	mgr.ProjectionDir = t.TempDir()
	manifest := strings.NewReplacer(`"acme"`, `"acme-delivery"`, `command = "claude"`, `command = "`+bin+`"`).Replace(sessionIDManifest)
	m, err := api.ParseManifestBytes([]byte(manifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	t.Cleanup(func() { _ = mgr.CleanupWorkspace("acme-delivery") })

	sess := &api.Session{
		Name: "squad-reviewer-g1-0", Workspace: "acme-delivery", Team: "squad", Role: "reviewer",
		Runtime: api.Runtime{Name: "claude", Command: bin},
	}
	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create: %v", err)
	}
	stored, err := store.GetSession(sess.Key())
	if err != nil {
		t.Fatal(err)
	}
	if stored.SettingSources != "user,project,local" || stored.SettingSourcesDelivery != api.SettingSourcesArgv {
		t.Errorf("stored sources %q delivery %q, want user,project,local and argv", stored.SettingSources, stored.SettingSourcesDelivery)
	}
}
