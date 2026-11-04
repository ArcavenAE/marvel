package session

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/runtime"
	"github.com/arcavenae/marvel/internal/tmux"
)

const resumeManifest = `
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

    [team.role.shift]
    on = "context-pressure"
    headroom_tokens = 120000
    handoff = "/handoffs/{session}.md"
    handoff_marker = "END HANDOFF"

  [[team.role]]
  name = "coder"
  replicas = 1

    [team.role.runtime]
    image = "codex"
    command = "codex"
`

// resumeManager is a Manager with a bolt-backed store and no tmux driver:
// recordResume reads the store and writes the journal, and nothing else.
func resumeManager(t *testing.T) *Manager {
	t.Helper()
	store := api.NewStore()
	if err := store.OpenBolt(filepath.Join(t.TempDir(), "marvel.bolt")); err != nil {
		t.Fatalf("OpenBolt: %v", err)
	}
	t.Cleanup(func() { _ = store.CloseBolt() })
	fixed := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	mgr := &Manager{
		store:         store,
		adapters:      runtime.NewRegistry(),
		ProjectionDir: t.TempDir(),
		Events:        events.NewRing(16),
		Clock:         func() time.Time { return fixed },
	}
	m, err := api.ParseManifestBytes([]byte(resumeManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return mgr
}

func journalOf(t *testing.T, m *Manager) map[string]api.ResumeEntry {
	t.Helper()
	list, err := m.store.ResumeJournal()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]api.ResumeEntry{}
	for _, e := range list {
		out[e.Session] = e
	}
	return out
}

func TestLaunchRecordsTheResumeEntry(t *testing.T) {
	t.Parallel()
	mgr := resumeManager(t)
	sess := sessionFor("reviewer", "claude")
	sess.WorkDir = "/work/reviewer"
	sess.Predecessor = "acme/squad-reviewer-old"
	mgr.planLaunch(sess)

	mgr.recordResume(sess)

	e, ok := journalOf(t, mgr)[sess.Key()]
	if !ok {
		t.Fatalf("no journal entry for %s", sess.Key())
	}
	if e.ResumeID == "" || e.ResumeID != sess.HarnessSessionID {
		t.Fatalf("resume id = %q, want the id the launch assigned (%q)", e.ResumeID, sess.HarnessSessionID)
	}
	if e.HandoffPath != "/handoffs/"+sess.Name+".md" {
		t.Fatalf("handoff path = %q", e.HandoffPath)
	}
	if e.WorkDir != "/work/reviewer" || e.Predecessor != "acme/squad-reviewer-old" ||
		e.Role != "reviewer" || e.Team != "squad" || e.Workspace != "acme" {
		t.Fatalf("entry = %+v, missing a launch field", e)
	}
	if !e.LaunchedAt.Equal(time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("launched_at = %v", e.LaunchedAt)
	}
}

// A relaunch mints a new id, and the journal then holds the new one only.
func TestRelaunchReplacesTheResumeID(t *testing.T) {
	t.Parallel()
	mgr := resumeManager(t)
	sess := sessionFor("reviewer", "claude")
	mgr.planLaunch(sess)
	first := sess.HarnessSessionID
	mgr.recordResume(sess)

	mgr.planLaunch(sess)
	mgr.recordResume(sess)

	got := journalOf(t, mgr)
	if len(got) != 1 || got[sess.Key()].ResumeID == first || got[sess.Key()].ResumeID != sess.HarnessSessionID {
		t.Fatalf("journal = %+v, want one entry holding the second id (first was %q)", got, first)
	}
}

// An adapter with no id still has an entry: the seat's place and handoff are
// what a wake from the last handoff needs.
func TestLaunchWithNoResumeIDStillRecordsTheSeat(t *testing.T) {
	t.Parallel()
	mgr := resumeManager(t)
	sess := sessionFor("coder", "codex")
	mgr.planLaunch(sess)

	mgr.recordResume(sess)

	e, ok := journalOf(t, mgr)[sess.Key()]
	if !ok || e.ResumeID != "" || e.HandoffPath != "" {
		t.Fatalf("entry = %+v ok=%v, want an entry with no resume id and no handoff path", e, ok)
	}
}

// "Written on every launch" is the journal's headline, so this goes through
// Create, the one launch path, and fails if Create stops recording. The tests
// above call recordResume directly and would stay green without that call.
func TestCreateWritesTheResumeJournal(t *testing.T) {
	skipIfNoTmux(t)

	store := api.NewStore()
	if err := store.OpenBolt(filepath.Join(t.TempDir(), "marvel.bolt")); err != nil {
		t.Fatalf("OpenBolt: %v", err)
	}
	t.Cleanup(func() { _ = store.CloseBolt() })
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	mgr := NewManager(store, driver)

	ws := "test-resume-create"
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })

	sess := &api.Session{
		Name:      "agent-0",
		Workspace: ws,
		Team:      "agents",
		Role:      "worker",
		WorkDir:   t.TempDir(),
		Runtime:   api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
	}
	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	list, err := store.ResumeJournal()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Session != sess.Key() || list[0].Role != "worker" ||
		list[0].Runtime != "sleep" || list[0].WorkDir != sess.WorkDir || list[0].LaunchedAt.IsZero() {
		t.Fatalf("journal after Create = %+v, want one entry for %s", list, sess.Key())
	}
}
