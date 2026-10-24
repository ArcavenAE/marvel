package api

import (
	"errors"
	"testing"
	"time"
)

func tokenSession(t *testing.T, s *Store, name string, bind bool) (Session, string) {
	t.Helper()
	sess := &Session{
		Name: name, Workspace: "ws", Team: "squad", Role: "worker",
		Runtime: Runtime{Name: "simulator", Command: "simulator"},
		State:   SessionRunning, CreatedAt: time.Now().UTC(),
	}
	token := ""
	if bind {
		tok, hash, err := NewHeartbeatToken()
		if err != nil {
			t.Fatal(err)
		}
		sess.HeartbeatToken, sess.HeartbeatTokenHash, token = tok, hash, tok
	}
	if err := s.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	return *sess, token
}

// A handoff.put names no session, so the token has to find exactly the one it
// was minted for, and nothing else may.
func TestSessionByHeartbeatTokenFindsOnlyTheBoundSession(t *testing.T) {
	s := NewStore()
	a, aTok := tokenSession(t, s, "squad-worker-g1-0", true)
	b, bTok := tokenSession(t, s, "squad-worker-g1-1", true)
	_, _ = tokenSession(t, s, "squad-worker-legacy", false)

	for _, tc := range []struct {
		name  string
		token string
		want  string
		err   bool
	}{
		{"own token", aTok, a.Key(), false},
		{"sibling token", bTok, b.Key(), false},
		{"unknown token", "not-a-token", "", true},
		{"empty token never matches an unbound record", "", "", true},
	} {
		got, err := s.SessionByHeartbeatToken(tc.token)
		if tc.err {
			if !errors.Is(err, ErrHeartbeatUnauthorized) {
				t.Errorf("%s: err = %v, want ErrHeartbeatUnauthorized", tc.name, err)
			}
			continue
		}
		if err != nil || got.Key() != tc.want {
			t.Errorf("%s: got (%q, %v), want %q", tc.name, got.Key(), err, tc.want)
		}
	}
}
