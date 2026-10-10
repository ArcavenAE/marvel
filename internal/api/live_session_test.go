package api

import (
	"errors"
	"testing"
)

// TestAuthenticateLiveSession is the matrix for callers that must fail
// closed. The heartbeat path admits a record that carries no token hash, so
// a record with none is the row that matters here.
func TestAuthenticateLiveSession(t *testing.T) {
	t.Parallel()

	s := NewStore()
	selfKey, selfToken := mintedSession(t, s, "agent-0")
	_, peerToken := mintedSession(t, s, "agent-1")

	legacy := &Session{
		Name: "agent-legacy", Workspace: "ws", Team: "squad", Role: "worker",
		Runtime: Runtime{Name: "simulator", Command: "simulator"}, State: SessionRunning,
	}
	if err := s.CreateSession(legacy); err != nil {
		t.Fatal(err)
	}

	crashedKey, crashedToken := mintedSession(t, s, "agent-crashed")
	if err := s.UpdateSession(crashedKey, func(l *Session) error { l.State = SessionCrashed; return nil }); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		key, token string
		wantErr    error
	}{
		{"own token", selfKey, selfToken, nil},
		{"a peer's token", selfKey, peerToken, ErrHeartbeatUnauthorized},
		{"no token", selfKey, "", ErrHeartbeatUnauthorized},
		{"no hash on the record", legacy.Key(), "", ErrHeartbeatUnauthorized},
		{"a token for a record with no hash", legacy.Key(), selfToken, ErrHeartbeatUnauthorized},
		{"a session that is not running", crashedKey, crashedToken, ErrHeartbeatUnauthorized},
		{"an unknown session", "ws/nobody", selfToken, ErrNotFound},
	}
	for _, c := range cases {
		got, err := s.AuthenticateLiveSession(c.key, c.token)
		if !errors.Is(err, c.wantErr) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.wantErr)
			continue
		}
		if c.wantErr == nil && (got.Team != "squad" || got.Role != "worker" || got.Key() != c.key) {
			t.Errorf("%s: session %s team %q role %q, want the snapshot of %s", c.name, got.Key(), got.Team, got.Role, c.key)
		}
	}
}
