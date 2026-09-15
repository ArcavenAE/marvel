package session

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/runtime"
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
