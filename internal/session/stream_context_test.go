package session

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	rtevents "github.com/arcavenae/marvel/internal/runtime/events"
	"github.com/arcavenae/marvel/internal/tmux"
	"github.com/arcavenae/marvel/internal/usage"
)

// contextFixture is a claude-code stream-json transcript carrying
// per-assistant-line usage across three distinct message ids, which is
// where live context occupancy comes from. The numbers are the real
// tool_call fixture's: levels 33377, 33481, 34136 against a 1M window,
// with terminal totals that sum to 100994.
const contextFixture = `{"type":"system","subtype":"init","session_id":"fixture-ctx","cwd":"/tmp","model":"claude-fable-5[1m]"}
{"type":"assistant","session_id":"fixture-ctx","message":{"id":"msg_a","model":"claude-fable-5","role":"assistant","content":[{"type":"text","text":"one"}],"usage":{"input_tokens":11368,"output_tokens":3,"cache_read_input_tokens":16643,"cache_creation_input_tokens":5366}}}
{"type":"assistant","session_id":"fixture-ctx","message":{"id":"msg_b","model":"claude-fable-5","role":"assistant","content":[{"type":"text","text":"two"}],"usage":{"input_tokens":2,"output_tokens":2,"cache_read_input_tokens":22009,"cache_creation_input_tokens":11470}}}
{"type":"assistant","session_id":"fixture-ctx","message":{"id":"msg_c","model":"claude-fable-5","role":"assistant","content":[{"type":"text","text":"three"}],"usage":{"input_tokens":331,"output_tokens":1,"cache_read_input_tokens":33479,"cache_creation_input_tokens":326}}}
{"type":"result","subtype":"success","is_error":false,"session_id":"fixture-ctx","duration_ms":4816,"num_turns":3,"stop_reason":"end_turn","total_cost_usd":0.4264,"usage":{"input_tokens":11701,"output_tokens":455,"cache_read_input_tokens":72131,"cache_creation_input_tokens":17162},"modelUsage":{"claude-fable-5[1m]":{"inputTokens":11701,"outputTokens":455,"cacheReadInputTokens":72131,"cacheCreationInputTokens":17162,"costUSD":0.4264,"contextWindow":1000000,"maxOutputTokens":64000}},"permission_denials":[]}
`

// codexContextFixture has no model and no window anywhere, which is the
// unresolved-denominator case.
const codexContextFixture = `{"type":"thread.started","thread_id":"fixture-cx-ctx"}
{"type":"turn.started"}
{"type":"turn.completed","usage":{"input_tokens":13992,"cached_input_tokens":11008,"cache_write_input_tokens":0,"output_tokens":5,"reasoning_output_tokens":0}}
`

// runObservedHarness drives one stubbed harness through the whole byte
// path with a real accountant over a real store.
func runObservedHarness(t *testing.T, image, transcript string, window int) (*api.Store, *usage.Accountant, *events.Ring, *Manager, *api.Session) {
	t.Helper()
	skipIfNoTmux(t)

	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	ring := events.NewRing(200)
	acct := usage.New(store, usage.NewResolver(usage.DefaultTable()), usage.WithEvents(ring))

	mgr := NewManager(store, driver)
	mgr.Events = ring
	mgr.Usage = acct
	mgr.StreamDir = filepath.Join(t.TempDir(), "streams")

	ws := "test-" + image + "-context"
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })
	command := stubHarness(t, transcript)

	if err := store.CreateWorkspace(&api.Workspace{Name: ws}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	rt := api.Runtime{
		Name: image, Command: command, Mode: api.RuntimeModeHeadless,
		Prompt: "say ok", ContextWindow: window,
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
	return store, acct, ring, mgr, sess
}

// waitForContext polls the store until a reading satisfying want arrives.
func waitForContext(t *testing.T, store *api.Store, key string, want func(api.SessionContext) bool) api.SessionContext {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last api.SessionContext
	for time.Now().Before(deadline) {
		sess, err := store.GetSession(key)
		if err == nil {
			last = sess.SessionContext
			if want(last) {
				return last
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("no matching context reading within the deadline; last was %+v", last)
	return last
}

// TestClaudeStreamLightsTheContextColumn is the end-to-end proof for
// aae-orc-w5su: a real headless claude launch populates CTX% from parsed
// harness output, with no cooperative heartbeat anywhere.
func TestClaudeStreamLightsTheContextColumn(t *testing.T) {
	store, acct, _, mgr, sess := runObservedHarness(t, "claude", contextFixture, 0)

	got := waitForContext(t, store, sess.Key(), func(c api.SessionContext) bool {
		return c.ContextRequests == 3
	})

	// The level after three requests, NOT the 100994 the result line sums.
	if got.ContextTokens != 34136 {
		t.Errorf("tokens = %d, want 34136 (the last level, not the session total)", got.ContextTokens)
	}
	if got.ContextLimit != 1_000_000 {
		t.Errorf("limit = %d, want 1000000", got.ContextLimit)
	}
	if got.ContextPercent < 3.4 || got.ContextPercent > 3.42 {
		t.Errorf("percent = %v, want about 3.41", got.ContextPercent)
	}
	if got.ContextModel != "claude-fable-5[1m]" {
		t.Errorf("model = %q, want claude-fable-5[1m]", got.ContextModel)
	}
	if got.ContextAt.IsZero() {
		t.Error("ContextAt not stamped; the renderer would show absence")
	}
	// No heartbeat was ever sent: this is the whole point of the ticket.
	live, _ := store.GetSession(sess.Key())
	if !live.LastHeartbeat.IsZero() {
		t.Error("the stream path stamped LastHeartbeat")
	}

	// The accountant's own view agrees, and reconciliation is clean.
	occ, ok := acct.SessionOccupancy(sess.Key())
	if !ok {
		t.Fatal("accountant has no occupancy for the session")
	}
	if occ.Tokens != 34136 {
		t.Errorf("accountant tokens = %d, want 34136", occ.Tokens)
	}
	if s := acct.Stats(); s.CumulationViolations != 0 || s.ReconcileMismatches != 0 {
		t.Errorf("reconciliation flagged a correct session: %+v", s)
	}

	if err := mgr.Delete(sess.Key()); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	// Deleting the session retires its accounting.
	if _, ok := acct.SessionOccupancy(sess.Key()); ok {
		t.Error("accountant state outlived its session")
	}
}

// TestUnresolvedWindowReportsAbsenceEndToEnd: codex names no model and no
// window, so tokens are real and the percentage is not invented.
func TestUnresolvedWindowReportsAbsenceEndToEnd(t *testing.T) {
	store, _, ring, mgr, sess := runObservedHarness(t, "codex", codexContextFixture, 0)

	got := waitForContext(t, store, sess.Key(), func(c api.SessionContext) bool {
		return c.ContextRequests > 0
	})

	// Subsumptive layout: input_tokens already contains the cached tokens.
	if got.ContextTokens != 13992 {
		t.Errorf("tokens = %d, want 13992", got.ContextTokens)
	}
	if got.ContextLimit != 0 {
		t.Errorf("limit = %d, want 0 (no model, no window, no guess)", got.ContextLimit)
	}
	if got.ContextPercent != 0 {
		t.Errorf("percent = %v, want 0", got.ContextPercent)
	}
	if got.ContextAt.IsZero() {
		t.Error("ContextAt not stamped; measured-without-a-window must be distinguishable from never-measured")
	}
	waitForKind(t, ring, events.KindContextLimitUnresolved, 10*time.Second)

	if err := mgr.Delete(sess.Key()); err != nil {
		t.Fatalf("delete session: %v", err)
	}
}

// The operator's escape hatch: one manifest line resolves the window for
// a model marvel's table does not know.
func TestManifestWindowResolvesUnknownModel(t *testing.T) {
	store, _, _, mgr, sess := runObservedHarness(t, "codex", codexContextFixture, 258_400)

	got := waitForContext(t, store, sess.Key(), func(c api.SessionContext) bool {
		return c.ContextRequests > 0
	})
	if got.ContextLimit != 258_400 {
		t.Errorf("limit = %d, want 258400 from runtime.context_window", got.ContextLimit)
	}
	if got.ContextPercent < 5.4 || got.ContextPercent > 5.42 {
		t.Errorf("percent = %v, want about 5.41", got.ContextPercent)
	}
	if got.ContextLimitSource != "manifest" {
		t.Errorf("limit source = %q, want manifest", got.ContextLimitSource)
	}

	if err := mgr.Delete(sess.Key()); err != nil {
		t.Fatalf("delete session: %v", err)
	}
}

// gatedObserver records the order of accounting calls and can park the
// drain goroutine inside a fold, which is what makes the ordering
// deterministic rather than a race the test would usually win.
type gatedObserver struct {
	entered chan struct{}
	release chan struct{}

	mu    sync.Mutex
	calls []string
	once  bool
}

func newGatedObserver() *gatedObserver {
	return &gatedObserver{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
}

func (g *gatedObserver) Bind(usage.Coords, usage.Bind) {}

func (g *gatedObserver) Observe(_ usage.Coords, _ rtevents.Event) {
	g.mu.Lock()
	first := !g.once
	g.once = true
	g.mu.Unlock()
	if first {
		g.entered <- struct{}{}
		<-g.release
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, "observe")
}

func (g *gatedObserver) Forget(string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, "forget")
}

func (g *gatedObserver) snapshot() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.calls...)
}

// TestForgetFollowsTheBufferedTail: deleting a session mid-stream must
// retire its accounting AFTER the drain goroutine folds the last buffered
// event, never beside the delete.
//
// The events channel is 256 deep by design and stream teardown joins the
// parser goroutine only, so events already queued survive the delete. A
// Forget issued first is undone by them: the accountant recreates the
// session from nothing, so the team's retired totals count it twice, its
// Partial flag latches on a requestless state, and a live CTX% cell can be
// re-blanked with a windowless reading.
func TestForgetFollowsTheBufferedTail(t *testing.T) {
	skipIfNoTmux(t)

	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	obs := newGatedObserver()
	mgr := NewManager(store, driver)
	mgr.Usage = obs
	mgr.StreamDir = filepath.Join(t.TempDir(), "streams")

	ws := "test-forget-ordering"
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })
	command := stubHarness(t, contextFixture)
	headlessTeam(t, store, ws, command, "say ok")

	sess := &api.Session{
		Name: "squad-worker-g1-0", Workspace: ws, Team: "squad", Role: "worker",
		Runtime: api.Runtime{
			Name: "claude", Command: command,
			Mode: api.RuntimeModeHeadless, Prompt: "say ok",
		},
	}
	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Park the drain inside its first fold, so the rest of the transcript
	// queues in the channel buffer while the delete runs.
	select {
	case <-obs.entered:
	case <-time.After(15 * time.Second):
		t.Fatal("the drain goroutine never folded an event")
	}

	deleted := make(chan error, 1)
	go func() { deleted <- mgr.Delete(sess.Key()) }()

	time.Sleep(150 * time.Millisecond)
	close(obs.release)

	select {
	case err := <-deleted:
		if err != nil {
			t.Fatalf("delete session: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("delete never returned")
	}

	// Delete returns only once the drain has finished, so every fold this
	// session will ever produce is already recorded. The wanted shape is
	// N observes then exactly one trailing forget. A forget anywhere else,
	// or a short run of observes, means the tail landed after the retire
	// (or was thrown away with it).
	calls := obs.snapshot()
	observes := 0
	for _, c := range calls {
		if c == "observe" {
			observes++
		}
	}
	if observes < 3 || len(calls) != observes+1 || calls[len(calls)-1] != "forget" {
		t.Errorf("accounting calls = %v; want at least 3 folds, then one trailing forget", calls)
	}
}

// An interactive session produces no adapter events, so it produces no
// reading. Headless-only is the documented scope of this slice.
func TestInteractiveSessionHasNoContextReading(t *testing.T) {
	skipIfNoTmux(t)

	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	acct := usage.New(store, usage.NewResolver(usage.DefaultTable()))
	mgr := NewManager(store, driver)
	mgr.Usage = acct
	mgr.StreamDir = filepath.Join(t.TempDir(), "streams")

	ws := "test-interactive-context"
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })

	sess := &api.Session{
		Name: "shell-0", Workspace: ws, Team: "util", Role: "shell",
		Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"60"}},
	}
	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	live, err := store.GetSession(sess.Key())
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if !live.ContextAt.IsZero() {
		t.Errorf("an interactive session produced a context reading: %+v", live.SessionContext)
	}
	if err := mgr.Delete(sess.Key()); err != nil {
		t.Fatalf("delete session: %v", err)
	}
}
