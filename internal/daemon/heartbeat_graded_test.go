package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// registerGradedSession puts a bound session in the store with a chosen
// harness and manifest window, so a test can exercise the real
// usage.Resolver the daemon wires into the store at construction. It
// returns the key and the plaintext token the way the agent's environment
// receives it.
func registerGradedSession(t *testing.T, d *Daemon, name, harness string, manifestLimit int) (key, token string) {
	t.Helper()
	tok, hash, err := api.NewHeartbeatToken()
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	sess := &api.Session{
		Name:      name,
		Workspace: "ws",
		Team:      "squad",
		Role:      "worker",
		Runtime: api.Runtime{
			Name:          harness,
			Command:       harness,
			ContextWindow: manifestLimit,
		},
		State:              api.SessionRunning,
		HeartbeatTokenHash: hash,
		CreatedAt:          time.Now().UTC(),
	}
	if err := d.store.CreateSession(sess); err != nil {
		t.Fatalf("create session %s: %v", name, err)
	}
	return sess.Key(), tok
}

// TestGradedHeartbeatResolvesThroughTheRealLadder is the composition proof
// for aae-orc-38yr: a producer's real wire shape, carrying the numerator
// and the denominator, run through the real handler against the real
// usage.Resolver the daemon shares with the accountant. It asserts the two
// halves the store-level fake cannot: that the daemon actually WIRED a
// resolver into the store, and that the resolver is the genuine ladder
// (feed below manifest), not a stand-in.
func TestGradedHeartbeatResolvesThroughTheRealLadder(t *testing.T) {
	d := newHandlerDaemon(t)

	t.Run("feed window resolves when no operator override is set", func(t *testing.T) {
		key, token := registerGradedSession(t, d, "agent-feed", "claude", 0)
		req := api.HeartbeatRequest{
			SessionKey:     key,
			SessionToken:   token,
			ContextTokens:  120_000,
			ContextWindow:  200_000,
			Model:          "claude-opus-4-8",
			ContextPercent: 60,
		}
		raw, _ := json.Marshal(req)
		if resp := d.handleHeartbeat(raw); resp.Error != "" {
			t.Fatalf("handleHeartbeat: %q", resp.Error)
		}
		got, _ := d.store.GetSession(key)
		if got.ContextSource != api.ContextSourceHeartbeat {
			t.Fatalf("ContextSource = %q, want heartbeat", got.ContextSource)
		}
		if got.ContextLimit != 200_000 || got.ContextLimitSource != "feed" {
			t.Fatalf("limit=%d source=%q, want 200000 / feed", got.ContextLimit, got.ContextLimitSource)
		}
		if got.ContextTokens != 120_000 {
			t.Fatalf("ContextTokens = %d, want 120000", got.ContextTokens)
		}
		if got.ContextPercent != 60 {
			t.Fatalf("ContextPercent = %.2f, want 60 (derived 120000/200000)", got.ContextPercent)
		}
	})

	t.Run("operator manifest override outranks the feed window", func(t *testing.T) {
		key, token := registerGradedSession(t, d, "agent-manifest", "claude", 500_000)
		req := api.HeartbeatRequest{
			SessionKey:    key,
			SessionToken:  token,
			ContextTokens: 120_000,
			ContextWindow: 200_000, // the feed's window, which the manifest outranks
			Model:         "claude-opus-4-8",
		}
		raw, _ := json.Marshal(req)
		if resp := d.handleHeartbeat(raw); resp.Error != "" {
			t.Fatalf("handleHeartbeat: %q", resp.Error)
		}
		got, _ := d.store.GetSession(key)
		if got.ContextLimit != 500_000 || got.ContextLimitSource != "manifest" {
			t.Fatalf("limit=%d source=%q, want 500000 / manifest (the ruling has effect)",
				got.ContextLimit, got.ContextLimitSource)
		}
		if got.ContextPercent != 24 {
			t.Fatalf("ContextPercent = %.2f, want 24 (120000/500000, the operator's denominator)", got.ContextPercent)
		}
	})

	t.Run("a windowless simulator beat stays a percentage-only reading", func(t *testing.T) {
		key, token := registerGradedSession(t, d, "agent-sim", "simulator", 0)
		// The simulator's exact shape: percentage, no model, no numerator,
		// no window. It must keep the pre-38yr behavior.
		req := api.NewHeartbeatRequestWithToken(key, token, 40, "")
		raw, _ := json.Marshal(req)
		if resp := d.handleHeartbeat(raw); resp.Error != "" {
			t.Fatalf("handleHeartbeat: %q", resp.Error)
		}
		got, _ := d.store.GetSession(key)
		if got.ContextLimit != 0 || got.ContextLimitSource != "" || got.ContextTokens != 0 {
			t.Fatalf("percentage-only beat leaked a window: limit=%d src=%q tokens=%d",
				got.ContextLimit, got.ContextLimitSource, got.ContextTokens)
		}
		if got.ContextPercent != 40 || got.ContextSource != api.ContextSourceHeartbeat {
			t.Fatalf("pct=%.2f source=%q, want 40 / heartbeat", got.ContextPercent, got.ContextSource)
		}
	})
}
