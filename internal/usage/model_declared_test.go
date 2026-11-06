package usage

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/runtime/opencode"
)

// A role's declared model (runtime.model) is one more way the resolver learns
// the model, after the stream and before the launch args
// (docs/design/role-runtime-model.md, accounting).

func TestResolveKeysOnDeclaredModel(t *testing.T) {
	t.Parallel()
	r := NewResolver(Table{"claude-sonnet-5-5": 200_000})
	limit, src, _, model := r.ResolveGraded(Request{Harness: "claude", DeclaredModel: "claude-sonnet-5-5", Redirection: api.BackendDefault})
	if model != "claude-sonnet-5-5" {
		t.Errorf("model = %q, want the declared one", model)
	}
	if limit != 200_000 || src != LimitFromTable {
		t.Errorf("got (%d, %q), want (200000, %q)", limit, src, LimitFromTable)
	}
}

func TestResolveStreamModelOutranksDeclared(t *testing.T) {
	t.Parallel()
	r := NewResolver(Table{"claude-sonnet-5-5": 200_000, "claude-opus-5": 500_000})
	limit, _, _, model := r.ResolveGraded(Request{
		Harness: "claude", StreamModel: "claude-opus-5", DeclaredModel: "claude-sonnet-5-5", Redirection: api.BackendDefault,
	})
	if model != "claude-opus-5" || limit != 500_000 {
		t.Errorf("got (%d, %q), want the stream model's (500000, claude-opus-5)", limit, model)
	}
}

func TestDeclaredModelOutranksLaunchArgs(t *testing.T) {
	t.Parallel()
	r := NewResolver(Table{"claude-sonnet-5-5": 200_000, "claude-opus-5": 500_000})
	_, _, _, model := r.ResolveGraded(Request{
		Harness: "claude", DeclaredModel: "claude-sonnet-5-5", RuntimeArgs: []string{"--model", "claude-opus-5"}, Redirection: api.BackendDefault,
	})
	if model != "claude-sonnet-5-5" {
		t.Errorf("model = %q, want the declared one ahead of the args", model)
	}
}

func TestModelFromEnvReadsAnthropicModelOnClaudeOnly(t *testing.T) {
	t.Parallel()
	r := NewResolver(Table{"claude-sonnet-5-5": 200_000})
	env := map[string]string{"ANTHROPIC_MODEL": "claude-sonnet-5-5"}
	if _, _, _, model := r.ResolveGraded(Request{Harness: "claude", RuntimeEnv: env, Redirection: api.BackendDefault}); model != "claude-sonnet-5-5" {
		t.Errorf("claude model = %q, want it read from ANTHROPIC_MODEL", model)
	}
	if _, _, _, model := r.ResolveGraded(Request{Harness: "codex", RuntimeEnv: env, Redirection: api.BackendDefault}); model != "" {
		t.Errorf("codex model = %q, want none: the variable is claude's", model)
	}
}

// An id the table does not know stays unresolved: never a default window, and
// the one event says which model was asked for.
func TestResolveUnknownDeclaredModelStaysUnresolved(t *testing.T) {
	t.Parallel()
	a, sink, ring := newTestAccountant(t, Table{"claude-sonnet-5-5": 200_000})
	a.Bind(testCoords, Bind{Harness: "claude", DeclaredModel: "claude-future-9", Redirection: api.BackendDefault})
	a.Observe(testCoords, turnEvent("claude", additive(13_992, 0, 0)))

	got, ok := sink.get(testCoords.AgentID)
	if !ok {
		t.Fatal("no reading written")
	}
	if got.ContextLimit != 0 || got.ContextLimitSource != string(LimitUnresolved) {
		t.Errorf("limit = %d from %q, want an unresolved window", got.ContextLimit, got.ContextLimitSource)
	}
	snap := ring.Snapshot(events.Filter{Kind: events.KindContextLimitUnresolved}, 0)
	if len(snap) != 1 {
		t.Fatalf("got %d context.limit-unresolved events, want 1", len(snap))
	}
	if !strings.Contains(snap[0].Message, "claude-future-9") {
		t.Errorf("event %q does not name the declared model", snap[0].Message)
	}
}

// A declared id keys the table only where a direct-API window applies: on a
// redirected backend it keys nothing.
func TestResolveDeclaredModelRefusedUnderRedirection(t *testing.T) {
	t.Parallel()
	r := NewResolver(Table{"claude-haiku-4-5": 200_000})
	req := Request{Harness: "claude", DeclaredModel: "claude-haiku-4-5"}

	req.Redirection = api.BackendDefault
	if limit, _, _, _ := r.ResolveGraded(req); limit != 200_000 {
		t.Fatalf("control: the declared model on the default backend resolved %d, want 200000", limit)
	}
	req.Redirection = api.BackendRedirected
	limit, src, key, _ := r.ResolveGraded(req)
	if limit != 0 || src != LimitUnresolved || key != KeyUndeterminable {
		t.Errorf("redirected: got (%d, %q, %q), want an unresolved, undeterminable resolution", limit, src, key)
	}
}

func TestCatalogLimitReadsDeclaredModel(t *testing.T) {
	t.Parallel()
	a, sink, _ := newTestAccountant(t, Table{})
	a.opencodeModelsPath = opencodeFixture

	a.Bind(testCoords, Bind{Harness: opencode.Harness, DeclaredModel: "opencode/claude-haiku-4-5", Redirection: api.BackendDefault})
	a.Observe(testCoords, turnEvent(opencode.Harness, additive(20_000, 0, 0)))

	got, ok := sink.get(testCoords.AgentID)
	if !ok {
		t.Fatal("no reading written")
	}
	if got.ContextLimit != 200_000 || got.ContextLimitSource != string(LimitFromFeed) {
		t.Errorf("limit = %d from %q, want 200000 from the catalog entry for the declared model", got.ContextLimit, got.ContextLimitSource)
	}
}
