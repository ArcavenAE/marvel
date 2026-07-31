package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/tmux"
)

// codexFixture is a trimmed codex `exec --json` stream a stub replays:
// thread.started, turn.started, one agent_message, turn.completed.
const codexFixture = `{"type":"thread.started","thread_id":"fixture-cx"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"ok"}}
{"type":"turn.completed","usage":{"input_tokens":100,"output_tokens":5}}
`

// opencodeFixture is a trimmed opencode `run --format json` stream:
// step_start, text, step_finish.
const opencodeFixture = `{"type":"step_start","sessionID":"ses_fx","part":{"type":"step-start"}}
{"type":"text","sessionID":"ses_fx","part":{"type":"text","text":"ok"}}
{"type":"step_finish","sessionID":"ses_fx","part":{"type":"step-finish","tokens":{"input":100,"output":2},"cost":0}}
`

// headlessHarnessTeam registers a workspace + team whose single role runs
// the named harness headlessly with the given command.
func headlessHarnessTeam(t *testing.T, store *api.Store, ws, image, command, prompt string) {
	t.Helper()
	if err := store.CreateWorkspace(&api.Workspace{Name: ws}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	rt := api.Runtime{Name: image, Command: command, Mode: api.RuntimeModeHeadless, Prompt: prompt}
	err := store.CreateTeam(&api.Team{
		Name:      "squad",
		Workspace: ws,
		Roles:     []api.Role{{Name: "worker", Replicas: 1, Runtime: rt}},
	})
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
}

// runStubbedHarness drives one stubbed harness session through the whole
// byte path and returns the ring for assertions.
func runStubbedHarness(t *testing.T, image, transcript string) (*events.Ring, *Manager, *api.Session) {
	t.Helper()
	skipIfNoTmux(t)

	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	ring := events.NewRing(200)
	mgr := NewManager(store, driver)
	mgr.Events = ring
	mgr.StreamDir = filepath.Join(t.TempDir(), "streams")

	ws := "test-" + image + "-wiring"
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })
	command := stubHarness(t, transcript)
	headlessHarnessTeam(t, store, ws, image, command, "say ok")

	sess := &api.Session{
		Name:      "squad-worker-g1-0",
		Workspace: ws,
		Team:      "squad",
		Role:      "worker",
		Runtime:   api.Runtime{Name: image, Command: command, Mode: api.RuntimeModeHeadless, Prompt: "say ok"},
	}
	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if mgr.Instance(sess.Key()) == nil {
		t.Fatal("expected an instance for a stream-capable session")
	}
	return ring, mgr, sess
}

func TestManagerStreamsCodexEventsIntoRing(t *testing.T) {
	ring, mgr, sess := runStubbedHarness(t, "codex", codexFixture)

	started := waitForKind(t, ring, events.KindAgentSessionStarted, 15*time.Second)
	if started.Workspace != sess.Workspace || started.Team != "squad" || started.Role != "worker" {
		t.Errorf("codex agent event not tagged with role coordinates: %+v", started)
	}
	if started.Session != sess.Key() {
		t.Errorf("session = %q, want %q", started.Session, sess.Key())
	}

	msg := waitForKind(t, ring, events.KindAgentMessageCompleted, 10*time.Second)
	if msg.Message != "assistant: ok" {
		t.Errorf("message = %q, want %q", msg.Message, "assistant: ok")
	}
	// Codex's terminal accounting rides turn.completed (no session.ended).
	waitForKind(t, ring, events.KindAgentTurnCompleted, 10*time.Second)

	if err := mgr.Delete(sess.Key()); err != nil {
		t.Fatalf("delete session: %v", err)
	}
}

func TestManagerStreamsOpenCodeEventsIntoRing(t *testing.T) {
	ring, mgr, sess := runStubbedHarness(t, "opencode", opencodeFixture)

	// OpenCode run mode has no session lifecycle line; the first agent
	// signal is the step boundary → turn.started.
	started := waitForKind(t, ring, events.KindAgentTurnStarted, 15*time.Second)
	if started.Workspace != sess.Workspace || started.Team != "squad" || started.Role != "worker" {
		t.Errorf("opencode agent event not tagged with role coordinates: %+v", started)
	}
	if started.Session != sess.Key() {
		t.Errorf("session = %q, want %q", started.Session, sess.Key())
	}

	msg := waitForKind(t, ring, events.KindAgentMessageCompleted, 10*time.Second)
	if msg.Message != "assistant: ok" {
		t.Errorf("message = %q, want %q", msg.Message, "assistant: ok")
	}

	if err := mgr.Delete(sess.Key()); err != nil {
		t.Fatalf("delete session: %v", err)
	}
}

// TestManagerSmokeRealCodex runs one live `codex exec --json` turn through
// the whole path. Off by default: it costs tokens and needs codex auth.
func TestManagerSmokeRealCodex(t *testing.T) {
	if os.Getenv("MARVEL_SMOKE") != "1" {
		t.Skip("set MARVEL_SMOKE=1 to run the live codex smoke")
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex not on PATH")
	}
	skipIfNoTmux(t)

	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	ring := events.NewRing(500)
	mgr := NewManager(store, driver)
	mgr.Events = ring
	mgr.StreamDir = filepath.Join(t.TempDir(), "streams")

	ws := "test-codex-smoke"
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })
	prompt := "Reply with the single word: ok"
	headlessHarnessTeam(t, store, ws, "codex", "codex", prompt)

	sess := &api.Session{
		Name:      "squad-worker-g1-0",
		Workspace: ws,
		Team:      "squad",
		Role:      "worker",
		Runtime:   api.Runtime{Name: "codex", Command: "codex", Mode: api.RuntimeModeHeadless, Prompt: prompt},
	}
	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	waitForKind(t, ring, events.KindAgentSessionStarted, 90*time.Second)
	ended := waitForKind(t, ring, events.KindAgentTurnCompleted, 120*time.Second)
	t.Logf("live codex turn.completed: %s", ended.Message)
	if err := mgr.Delete(sess.Key()); err != nil {
		t.Fatalf("delete session: %v", err)
	}
}

// TestManagerSmokeRealOpenCode runs one live `opencode run --format json`
// turn. Off by default; needs opencode plus an authorized model. The free
// opencode model is used so no cloud credential is required, but if the
// path emits nothing the test surfaces it rather than hanging silently.
func TestManagerSmokeRealOpenCode(t *testing.T) {
	if os.Getenv("MARVEL_SMOKE") != "1" {
		t.Skip("set MARVEL_SMOKE=1 to run the live opencode smoke")
	}
	if _, err := exec.LookPath("opencode"); err != nil {
		t.Skip("opencode not on PATH")
	}
	skipIfNoTmux(t)

	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	ring := events.NewRing(500)
	mgr := NewManager(store, driver)
	mgr.Events = ring
	mgr.StreamDir = filepath.Join(t.TempDir(), "streams")

	ws := "test-opencode-smoke"
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })
	prompt := "Reply with the single word: ok"

	if err := store.CreateWorkspace(&api.Workspace{Name: ws}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	rt := api.Runtime{
		Name:    "opencode",
		Command: "opencode",
		Args:    []string{"-m", "opencode/deepseek-v4-flash-free"},
		Mode:    api.RuntimeModeHeadless,
		Prompt:  prompt,
	}
	if err := store.CreateTeam(&api.Team{
		Name: "squad", Workspace: ws,
		Roles: []api.Role{{Name: "worker", Replicas: 1, Runtime: rt}},
	}); err != nil {
		t.Fatalf("create team: %v", err)
	}

	sess := &api.Session{
		Name: "squad-worker-g1-0", Workspace: ws, Team: "squad", Role: "worker",
		Runtime: rt,
	}
	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	// Any agent event proves the launch → FIFO → parser → ring path; the
	// first is the step boundary (turn.started).
	got := waitForKind(t, ring, events.KindAgentTurnStarted, 120*time.Second)
	t.Logf("live opencode turn.started: %+v", got)
	if err := mgr.Delete(sess.Key()); err != nil {
		t.Fatalf("delete session: %v", err)
	}
}
