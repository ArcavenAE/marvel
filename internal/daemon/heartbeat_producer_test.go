package daemon

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// TestEveryProducerShapeIsAdmitted is the boundary test whose absence let
// PRs #168 and #170 break each other. Both were green: #168 tested the
// daemon's auth behavior with params it constructed itself, and #170
// tested its payload decision without a daemon. Neither exercised a real
// producer's bytes against the real handler, which is the only place the
// break was visible.
//
// A test that builds heartbeatParams in-process cannot catch the next
// instance either, because the field it would omit is the field it does
// not know to set. So this drives api.NewHeartbeatRequest, the
// constructor every forwarder now calls, through handleHeartbeat against
// a session registered the way the manager registers one.
//
// A fourth forwarder is covered as long as it builds its payload with the
// constructor. That is the point of the constructor: the token is not a
// parameter it can forget to pass, it is read from the environment marvel
// already constructs at spawn.
func TestEveryProducerShapeIsAdmitted(t *testing.T) {
	d := newHandlerDaemon(t)
	boundKey, boundToken := registerSession(t, d, "agent-bound", true)

	t.Setenv(api.HeartbeatTokenEnv, boundToken)

	tests := []struct {
		name string
		// build produces the payload the way one real forwarder does.
		build func() api.HeartbeatRequest
	}{
		{
			// cmd/marvel/ctxforward.go, the claude statusline feed, now
			// carrying the numerator and denominator (aae-orc-38yr).
			name: "ctx-forward reads the token from the environment",
			build: func() api.HeartbeatRequest {
				p := api.NewHeartbeatRequest(boundKey, 61, "claude-opus-5")
				p.ContextTokens = 122_000
				p.ContextWindow = 200_000
				return p
			},
		},
		{
			// cmd/marvel/codexctx.go, the codex rollout hook. This is the
			// producer that was refused on the merge commit; it now carries
			// occupancy alongside the window and the token.
			name: "codex-ctx carries a window alongside the token",
			build: func() api.HeartbeatRequest {
				p := api.NewHeartbeatRequest(boundKey, 93.85, "gpt-5.6-sol")
				p.ContextTokens = 242_696
				p.ContextWindow = 258400
				return p
			},
		},
		{
			// cmd/simulator/main.go, which names no model.
			name: "simulator sends no model",
			build: func() api.HeartbeatRequest {
				return api.NewHeartbeatRequestWithToken(boundKey, boundToken, 40, "")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.build())
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if resp := d.handleHeartbeat(raw); resp.Error != "" {
				t.Fatalf("handleHeartbeat(%s) = %q, want admitted", raw, resp.Error)
			}
		})
	}
}

// TestABareMapPayloadIsRefused is the negative half, and without it the
// test above proves nothing: a handler that admitted everything would
// pass it. This is the exact payload cmd/marvel/codexctx.go sent on the
// merge commit, spelled as a map so it cannot silently acquire the field
// the shared type would give it.
func TestABareMapPayloadIsRefused(t *testing.T) {
	d := newHandlerDaemon(t)
	boundKey, _ := registerSession(t, d, "agent-bound", true)

	raw, err := json.Marshal(map[string]any{
		"session_key":     boundKey,
		"context_percent": 93.85,
		"model":           "gpt-5.6-sol",
		"context_window":  258400,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	resp := d.handleHeartbeat(raw)
	if !strings.Contains(resp.Error, "does not match") {
		t.Fatalf("handleHeartbeat(%s) = %q, want a refusal; a producer that omits the token must fail loudly", raw, resp.Error)
	}
}

// TestNewHeartbeatRequestReadsTheSpawnEnvironment pins the variable name.
// The constructor's whole guarantee is that a forwarder cannot forget the
// token, and that guarantee is only as good as this string matching what
// the adapter writes into the pane.
func TestNewHeartbeatRequestReadsTheSpawnEnvironment(t *testing.T) {
	t.Setenv(api.HeartbeatTokenEnv, "tok-abc")
	if got := api.NewHeartbeatRequest("ws/sess", 12, "m").SessionToken; got != "tok-abc" {
		t.Fatalf("SessionToken = %q, want it read from %s", got, api.HeartbeatTokenEnv)
	}
	if os.Getenv(api.HeartbeatTokenEnv) != "tok-abc" {
		t.Fatal("t.Setenv did not apply")
	}
}
