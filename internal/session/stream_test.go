package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/tmux"
)

// streamFixture is the claude-code stream-json a stub harness replays. It
// mirrors the shape of runtime/claudecode/testdata/hello.ndjson, trimmed
// to the three lines this test asserts on.
const streamFixture = `{"type":"system","subtype":"init","session_id":"fixture-1","cwd":"/tmp","model":"claude-haiku-4-5"}
{"type":"assistant","session_id":"fixture-1","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]}}
{"type":"result","subtype":"success","is_error":false,"session_id":"fixture-1","duration_ms":4816,"duration_api_ms":6546,"ttft_ms":4775,"num_turns":1,"stop_reason":"end_turn","total_cost_usd":0.0421,"usage":{"input_tokens":11368,"output_tokens":13},"modelUsage":{"claude-haiku-4-5":{"inputTokens":522,"outputTokens":13,"costUSD":0.000587}},"permission_denials":[]}
`

// stubHarness writes a canned stream-json transcript to stdout and exits,
// which is all a stream-capable adapter's command needs to do. It ignores
// the flags the adapter appends, exactly as a harness would accept them.
func stubHarness(t *testing.T, transcript string) string {
	t.Helper()
	dir := t.TempDir()
	fixture := filepath.Join(dir, "transcript.ndjson")
	if err := os.WriteFile(fixture, []byte(transcript), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	script := filepath.Join(dir, "stub-claude")
	body := fmt.Sprintf("#!/bin/sh\nexec cat %q\n", fixture)
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return script
}

// headlessTeam registers the workspace and team a stream-capable session
// needs: the adapter path only runs with full team/role context.
func headlessTeam(t *testing.T, store *api.Store, ws, command, prompt string) {
	t.Helper()
	if err := store.CreateWorkspace(&api.Workspace{Name: ws}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	rt := api.Runtime{
		Name:    "claude",
		Command: command,
		Mode:    api.RuntimeModeHeadless,
		Prompt:  prompt,
	}
	err := store.CreateTeam(&api.Team{
		Name:      "squad",
		Workspace: ws,
		Roles:     []api.Role{{Name: "worker", Replicas: 1, Runtime: rt}},
	})
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
}

// waitForKind polls the ring until an event of kind k arrives.
func waitForKind(t *testing.T, ring *events.Ring, k events.Kind, timeout time.Duration) events.Event {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		snap := ring.Snapshot(events.Filter{Kind: k}, 0)
		if len(snap) > 0 {
			return snap[len(snap)-1]
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("no %s event within %s; ring holds %+v", k, timeout, ring.Snapshot(events.Filter{}, 0))
	return events.Event{}
}

// TestManagerStreamsAgentEventsIntoRing is the wiring test for the whole
// byte path: adapter redirect → FIFO → parser → instance → ring.
func TestManagerStreamsAgentEventsIntoRing(t *testing.T) {
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

	ws := "test-stream-wiring"
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })
	headlessTeam(t, store, ws, stubHarness(t, streamFixture), "say ok")

	sess := &api.Session{
		Name:      "squad-worker-g1-0",
		Workspace: ws,
		Team:      "squad",
		Role:      "worker",
		Runtime: api.Runtime{
			Name:    "claude",
			Command: stubHarness(t, streamFixture),
			Mode:    api.RuntimeModeHeadless,
			Prompt:  "say ok",
		},
	}
	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if mgr.Instance(sess.Key()) == nil {
		t.Fatal("expected the manager to hold an instance for a stream-capable session")
	}

	started := waitForKind(t, ring, events.KindAgentSessionStarted, 15*time.Second)
	if started.Workspace != ws || started.Team != "squad" || started.Role != "worker" {
		t.Errorf("agent event not tagged with role coordinates: %+v", started)
	}
	if started.Session != sess.Key() {
		t.Errorf("session = %q, want %q", started.Session, sess.Key())
	}

	ended := waitForKind(t, ring, events.KindAgentSessionEnded, 15*time.Second)
	for _, want := range []string{"exit 0", "turns=1", "dur=4816ms", "cost=$0.0421"} {
		if !strings.Contains(ended.Message, want) {
			t.Errorf("session.ended message missing %q: %s", want, ended.Message)
		}
	}

	// The assistant message rides the same path.
	msg := waitForKind(t, ring, events.KindAgentMessageCompleted, 5*time.Second)
	if msg.Message != "assistant: ok" {
		t.Errorf("message = %q, want %q", msg.Message, "assistant: ok")
	}

	// Deleting the session retires the pipe with it.
	fifoPath := filepath.Join(mgr.StreamDir, ws+"-"+sess.Name+".ndjson")
	if err := mgr.Delete(sess.Key()); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, statErr := os.Stat(fifoPath); !os.IsNotExist(statErr) {
		t.Errorf("fifo %s survived session deletion: %v", fifoPath, statErr)
	}
	if mgr.Instance(sess.Key()) != nil {
		t.Error("instance outlived its session")
	}
}

// TestManagerInteractiveSessionHasNoStream pins the no-regression half:
// an interactive runtime still launches, still gets an instance, and gets
// no pipe and no agent events.
func TestManagerInteractiveSessionHasNoStream(t *testing.T) {
	skipIfNoTmux(t)

	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	ring := events.NewRing(50)
	mgr := NewManager(store, driver)
	mgr.Events = ring
	mgr.StreamDir = filepath.Join(t.TempDir(), "streams")

	ws := "test-stream-interactive"
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })

	sess := &api.Session{
		Name:      "shell-0",
		Workspace: ws,
		Team:      "util",
		Role:      "shell",
		Runtime:   api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"60"}},
	}
	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if sess.State != api.SessionRunning || sess.PaneID == "" {
		t.Fatalf("session did not come up: state=%s pane=%q", sess.State, sess.PaneID)
	}
	if _, statErr := os.Stat(mgr.StreamDir); statErr == nil {
		t.Error("a non-streaming session created a stream directory")
	}
	if len(ring.Snapshot(events.Filter{Kind: events.KindAgentSessionStarted}, 0)) != 0 {
		t.Error("a non-streaming session produced agent events")
	}
	if err := mgr.Delete(sess.Key()); err != nil {
		t.Fatalf("delete session: %v", err)
	}
}

// TestManagerSmokeRealClaude runs one short live request through the
// whole path. Off by default: it costs money, needs local auth, and its
// latency is the provider's, not ours. MARVEL_SMOKE=1 opts in.
func TestManagerSmokeRealClaude(t *testing.T) {
	if os.Getenv("MARVEL_SMOKE") != "1" {
		t.Skip("set MARVEL_SMOKE=1 to run the live claude smoke")
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

	ws := "test-stream-smoke"
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })
	headlessTeam(t, store, ws, "claude", "Reply with the single word: ok")

	sess := &api.Session{
		Name:      "squad-worker-g1-0",
		Workspace: ws,
		Team:      "squad",
		Role:      "worker",
		Runtime: api.Runtime{
			Name:    "claude",
			Command: "claude",
			Args:    []string{"--model", "haiku"},
			Mode:    api.RuntimeModeHeadless,
			Prompt:  "Reply with the single word: ok",
		},
	}
	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	waitForKind(t, ring, events.KindAgentSessionStarted, 60*time.Second)
	ended := waitForKind(t, ring, events.KindAgentSessionEnded, 120*time.Second)
	t.Logf("live session.ended: %s", ended.Message)
	if !strings.Contains(ended.Message, "exit 0") {
		t.Errorf("live run did not exit clean: %s", ended.Message)
	}
	if !strings.Contains(ended.Message, "dur=") {
		t.Errorf("live run reported no metering: %s", ended.Message)
	}
	if err := mgr.Delete(sess.Key()); err != nil {
		t.Fatalf("delete session: %v", err)
	}
}
