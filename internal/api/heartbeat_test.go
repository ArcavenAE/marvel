package api

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// mintedSession is a session as the manager spawns one: token in memory,
// digest on the record.
func mintedSession(t *testing.T, s *Store, name string) (key, token string) {
	t.Helper()
	tok, hash, err := NewHeartbeatToken()
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	sess := &Session{
		Name:               name,
		Workspace:          "ws",
		Team:               "squad",
		Role:               "worker",
		Runtime:            Runtime{Name: "simulator", Command: "simulator"},
		State:              SessionRunning,
		CreatedAt:          time.Now().UTC(),
		HeartbeatToken:     tok,
		HeartbeatTokenHash: hash,
	}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("create session %s: %v", name, err)
	}
	return sess.Key(), tok
}

// TestUpdateSessionHeartbeatAuthenticates is the authority matrix for the
// heartbeat RPC. The row that matters is peer: a token minted for one
// session, presented for another. Before the token existed, a heartbeat
// carried a session key and nothing else, so that row was indistinguishable
// from the agent reporting itself.
func TestUpdateSessionHeartbeatAuthenticates(t *testing.T) {
	t.Parallel()

	s := NewStore()
	selfKey, selfToken := mintedSession(t, s, "agent-0")
	peerKey, peerToken := mintedSession(t, s, "agent-1")

	// A record written before tokens existed: no hash to check against.
	legacy := &Session{
		Name: "agent-legacy", Workspace: "ws", Team: "squad", Role: "worker",
		Runtime: Runtime{Name: "simulator", Command: "simulator"},
		State:   SessionRunning, CreatedAt: time.Now().UTC(),
	}
	if err := s.CreateSession(legacy); err != nil {
		t.Fatalf("create legacy session: %v", err)
	}

	tests := []struct {
		name     string
		key      string
		token    string
		wantAuth HeartbeatAuth
		wantErr  error
	}{
		{
			name:     "own token admitted",
			key:      selfKey,
			token:    selfToken,
			wantAuth: HeartbeatAuthToken,
		},
		{
			name:    "peer token refused",
			key:     selfKey,
			token:   peerToken,
			wantErr: ErrHeartbeatUnauthorized,
		},
		{
			name:    "no token refused",
			key:     selfKey,
			token:   "",
			wantErr: ErrHeartbeatUnauthorized,
		},
		{
			name:    "hash of the token is not the token",
			key:     selfKey,
			token:   HashHeartbeatToken(selfToken),
			wantErr: ErrHeartbeatUnauthorized,
		},
		{
			name:    "unknown session refused",
			key:     "ws/nobody",
			token:   selfToken,
			wantErr: ErrNotFound,
		},
		{
			name:     "record with no hash admitted unbound",
			key:      legacy.Key(),
			token:    "",
			wantAuth: HeartbeatAuthUnbound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before, _ := s.GetSession(tt.key)
			auth, err := s.UpdateSessionHeartbeat(tt.key, tt.token, 42.5, "")

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want %v", err, tt.wantErr)
				}
				if auth != "" {
					t.Errorf("auth = %q on a refused heartbeat, want empty", auth)
				}
				// A refusal must not move the liveness clock: LastHeartbeat
				// feeds the healthcheck, the restart policy, and shift
				// readiness.
				after, gerr := s.GetSession(tt.key)
				if gerr != nil {
					return // unknown-session row has nothing to compare
				}
				if !after.LastHeartbeat.Equal(before.LastHeartbeat) {
					t.Errorf("refused heartbeat moved LastHeartbeat from %v to %v",
						before.LastHeartbeat, after.LastHeartbeat)
				}
				if after.ContextPercent != before.ContextPercent {
					t.Errorf("refused heartbeat wrote CTX%% %v, want %v",
						after.ContextPercent, before.ContextPercent)
				}
				return
			}

			if err != nil {
				t.Fatalf("UpdateSessionHeartbeat: %v", err)
			}
			if auth != tt.wantAuth {
				t.Fatalf("auth = %q, want %q", auth, tt.wantAuth)
			}
			got, _ := s.GetSession(tt.key)
			if got.ContextPercent != 42.5 {
				t.Errorf("CTX%% = %v after an admitted heartbeat, want 42.5", got.ContextPercent)
			}
			if got.LastHeartbeat.IsZero() {
				t.Error("an admitted heartbeat left LastHeartbeat zero")
			}
		})
	}

	// The peer's own beat still works: the refusal above is about binding,
	// not about a session poisoned by someone else's attempt.
	if _, err := s.UpdateSessionHeartbeat(peerKey, peerToken, 10, ""); err != nil {
		t.Fatalf("peer heartbeat with its own token: %v", err)
	}
}

// TestHeartbeatTokenNeverSerializes guards the property the whole fix
// rests on. Store records reach bbolt through encoding/json and reach
// every RPC client the same way, so a serialized plaintext token would be
// readable by exactly the sibling agents it separates.
func TestHeartbeatTokenNeverSerializes(t *testing.T) {
	t.Parallel()

	token, hash, err := NewHeartbeatToken()
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	sess := Session{
		Name: "agent-0", Workspace: "ws",
		HeartbeatToken: token, HeartbeatTokenHash: hash,
	}
	data, err := json.Marshal(sess)
	if err != nil {
		t.Fatalf("marshal session: %v", err)
	}
	if strings.Contains(string(data), token) {
		t.Fatal("the plaintext heartbeat token is serialized on the session record")
	}
	if !strings.Contains(string(data), hash) {
		t.Fatal("the token hash is not serialized, so an adopted session cannot be verified after restart")
	}

	// Rehydrate is the adoption path: the agent still holds the plaintext
	// in its environment, and the record must still be able to check it.
	var back Session
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal session: %v", err)
	}
	if back.HeartbeatTokenHash != hash {
		t.Fatalf("hash did not survive a round trip: %q", back.HeartbeatTokenHash)
	}
	if back.HeartbeatToken != "" {
		t.Fatalf("plaintext token came back from a round trip: %q", back.HeartbeatToken)
	}
}

func TestNewHeartbeatTokenIsUnique(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool, 64)
	for range 64 {
		token, hash, err := NewHeartbeatToken()
		if err != nil {
			t.Fatalf("mint token: %v", err)
		}
		if len(token) != 64 {
			t.Fatalf("token length = %d hex chars, want 64 (256 bits)", len(token))
		}
		if hash != HashHeartbeatToken(token) {
			t.Fatal("minted hash does not match the token's digest")
		}
		if seen[token] {
			t.Fatal("NewHeartbeatToken repeated a token")
		}
		seen[token] = true
	}

	if HashHeartbeatToken("") != "" {
		t.Error("the empty token acquired a digest, which an unbound record could then be matched against")
	}
}
