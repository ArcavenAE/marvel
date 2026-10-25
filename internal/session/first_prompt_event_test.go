package session

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/runtime"
	"github.com/arcavenae/marvel/internal/tmux"
)

// marvel#391, docs/design/interactive-first-prompt.md T6 (D7): each spawn emits
// one session.first-prompt event with the source and byte length of the first
// message it carried, and never the text.

const firstPromptManifest = `
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
    prompt = "SECRET-ROLE-PROMPT-TEXT"
`

func firstPromptManager(t *testing.T) (*Manager, *events.Ring) {
	t.Helper()
	mgr, ring := projectionManager(t)
	m, err := api.ParseManifestBytes([]byte(firstPromptManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := m.Apply(mgr.store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return mgr, ring
}

func reviewerSpawn(predecessor string) *api.Session {
	return &api.Session{
		Name:        "squad-reviewer-g2-0",
		Workspace:   "acme",
		Team:        "squad",
		Role:        "reviewer",
		Predecessor: predecessor,
		Runtime:     api.Runtime{Name: "claude", Command: "claude", Prompt: "SECRET-ROLE-PROMPT-TEXT"},
	}
}

// planLaunch hands the adapter's answer to the spawn, so the event can say it.
func TestPlanLaunchCarriesTheFirstPromptSource(t *testing.T) {
	t.Parallel()
	mgr, _ := firstPromptManager(t)

	if got := mgr.planLaunch(reviewerSpawn("")).firstPrompt; got.Source != runtime.FirstPromptRole || got.Bytes != len("SECRET-ROLE-PROMPT-TEXT") {
		t.Errorf("a first spawn's first prompt = %+v, want role, %d bytes", got, len("SECRET-ROLE-PROMPT-TEXT"))
	}
	got := mgr.planLaunch(reviewerSpawn("acme/squad-reviewer-g1-0")).firstPrompt
	if got.Source != runtime.FirstPromptSuccessorRole || got.Bytes <= len("SECRET-ROLE-PROMPT-TEXT") {
		t.Errorf("a successor's first prompt = %+v, want successor+role and more bytes than the role prompt", got)
	}
}

func TestEmitFirstPromptRecordsSourceAndLengthNeverText(t *testing.T) {
	t.Parallel()
	mgr, ring := firstPromptManager(t)
	sess := reviewerSpawn("acme/squad-reviewer-g1-0")

	mgr.emitFirstPrompt(sess, runtime.FirstPrompt{Source: runtime.FirstPromptSuccessorRole, Bytes: 207})

	got := ring.Snapshot(events.Filter{Kind: events.KindSessionFirstPrompt}, 0)
	if len(got) != 1 {
		t.Fatalf("session.first-prompt events = %d, want 1", len(got))
	}
	ev := got[0]
	if ev.Session != sess.Key() || ev.Workspace != "acme" || ev.Team != "squad" || ev.Role != "reviewer" {
		t.Errorf("event is not keyed to the session: %+v", ev)
	}
	for _, want := range []string{"successor+role", "207"} {
		if !strings.Contains(ev.Message, want) {
			t.Errorf("message %q lacks %q", ev.Message, want)
		}
	}
	if strings.Contains(ev.Message, "SECRET") || strings.Contains(ev.Message, "successor to") {
		t.Errorf("message carries prompt text: %q", ev.Message)
	}
}

// A launch that went by the direct fallback, with no adapter answer, carried no
// message marvel can describe: it still gets its one event, with source none.
func TestEmitFirstPromptForAnUnansweredLaunchSaysNone(t *testing.T) {
	t.Parallel()
	mgr, ring := firstPromptManager(t)
	mgr.emitFirstPrompt(reviewerSpawn(""), runtime.FirstPrompt{})
	got := ring.Snapshot(events.Filter{Kind: events.KindSessionFirstPrompt}, 0)
	if len(got) != 1 || !strings.Contains(got[0].Message, runtime.FirstPromptNone) || !strings.Contains(got[0].Message, "0 bytes") {
		t.Fatalf("events = %+v, want one saying none, 0 bytes", got)
	}
}

func newTmuxManager(t *testing.T, store *api.Store) (*Manager, *events.Ring) {
	t.Helper()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	mgr := NewManager(store, driver)
	ring := events.NewRing(64)
	mgr.Events = ring
	return mgr, ring
}

// Create emits it once per spawn, after the pane exists. This needs tmux.
func TestCreateEmitsExactlyOneFirstPromptEvent(t *testing.T) {
	skipIfNoTmux(t)
	store := api.NewStore()
	mgr, ring := newTmuxManager(t, store)
	sess := &api.Session{
		Name: "agent-0", Workspace: "test-first-prompt", Team: "agents", Role: "worker",
		Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}, Prompt: "not used by sleep"},
	}
	t.Cleanup(func() { _ = mgr.CleanupWorkspace("test-first-prompt") })
	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create: %v", err)
	}
	got := ring.Snapshot(events.Filter{Kind: events.KindSessionFirstPrompt}, 0)
	if len(got) != 1 {
		t.Fatalf("session.first-prompt events after one spawn = %d, want 1", len(got))
	}
	if got[0].Session != sess.Key() {
		t.Errorf("event session = %q, want %q", got[0].Session, sess.Key())
	}
}

// The first-prompt event describes a session, so it follows session.created
// and a reader keyed on created has already seen the session.
func TestAnnounceSpawnEmitsCreatedBeforeFirstPrompt(t *testing.T) {
	t.Parallel()
	mgr, ring := firstPromptManager(t)
	mgr.announceSpawn(reviewerSpawn(""), "%1", runtime.FirstPrompt{Source: runtime.FirstPromptRole, Bytes: 5})
	got := ring.Snapshot(events.Filter{}, 0)
	var kinds []string
	for _, ev := range got {
		if ev.Kind == events.KindSessionCreated || ev.Kind == events.KindSessionFirstPrompt {
			kinds = append(kinds, string(ev.Kind))
		}
	}
	want := []string{string(events.KindSessionCreated), string(events.KindSessionFirstPrompt)}
	if len(kinds) != 2 || kinds[0] != want[0] || kinds[1] != want[1] {
		t.Errorf("event order = %v, want %v", kinds, want)
	}
}
