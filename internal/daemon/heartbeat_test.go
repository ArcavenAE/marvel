package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// registerSession puts a session in the store the way the manager does at
// spawn: digest on the record, plaintext returned to the caller the way it
// reaches the agent's environment. No pane, because the heartbeat handler
// is synchronous store code and never touches tmux.
func registerSession(t *testing.T, d *Daemon, name string, bind bool) (key, token string) {
	t.Helper()
	sess := &api.Session{
		Name:      name,
		Workspace: "ws",
		Team:      "squad",
		Role:      "worker",
		Runtime:   api.Runtime{Name: "simulator", Command: "simulator"},
		State:     api.SessionRunning,
		CreatedAt: time.Now().UTC(),
	}
	if bind {
		tok, hash, err := api.NewHeartbeatToken()
		if err != nil {
			t.Fatalf("mint token: %v", err)
		}
		sess.HeartbeatToken = tok
		sess.HeartbeatTokenHash = hash
		token = tok
	}
	if err := d.store.CreateSession(sess); err != nil {
		t.Fatalf("create session %s: %v", name, err)
	}
	return sess.Key(), token
}

// TestHandleHeartbeatBindsToItsSession is the RPC-level authority matrix.
//
// Falsification: before the token, the handler took a session key off the
// wire and stamped it. Every row below except the first passed, so any
// process reaching the socket could hold a dead peer's liveness open and
// write the CTX% an operator reads.
func TestHandleHeartbeatBindsToItsSession(t *testing.T) {
	d := newHandlerDaemon(t)

	selfKey, selfToken := registerSession(t, d, "agent-0", true)
	_, peerToken := registerSession(t, d, "agent-1", true)
	legacyKey, _ := registerSession(t, d, "agent-legacy", false)

	tests := []struct {
		name      string
		key       string
		token     string
		wantErr   string
		wantEvent events.Kind
	}{
		{
			name: "own token admitted",
			key:  selfKey, token: selfToken,
		},
		{
			name: "peer token refused",
			key:  selfKey, token: peerToken,
			wantErr:   "does not match",
			wantEvent: events.KindHeartbeatRefused,
		},
		{
			name: "no token refused",
			key:  selfKey, token: "",
			wantErr:   "does not match",
			wantEvent: events.KindHeartbeatRefused,
		},
		{
			name: "unknown session refused",
			key:  "ws/nobody", token: selfToken,
			wantErr: "not found",
		},
		{
			name: "record with no hash admitted, and says so",
			key:  legacyKey, token: "",
			wantEvent: events.KindHeartbeatUnbound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := len(d.events.Snapshot(events.Filter{Kind: tt.wantEvent}, 0))

			p := heartbeatParams{SessionKey: tt.key, SessionToken: tt.token, ContextPercent: 61}
			resp := d.handleHeartbeat(mustMarshal(t, p))

			switch {
			case tt.wantErr == "" && resp.Error != "":
				t.Fatalf("heartbeat error = %q, want none", resp.Error)
			case tt.wantErr != "" && !strings.Contains(resp.Error, tt.wantErr):
				t.Fatalf("heartbeat error = %q, want it to contain %q", resp.Error, tt.wantErr)
			}

			if tt.wantEvent == "" {
				return
			}
			snap := d.events.Snapshot(events.Filter{Kind: tt.wantEvent}, 0)
			if len(snap) != before+1 {
				t.Fatalf("%s events = %d, want %d (the outcome was silent)",
					tt.wantEvent, len(snap), before+1)
			}
			ev := snap[len(snap)-1]
			if ev.Session != tt.key {
				t.Errorf("event names session %q, want %q", ev.Session, tt.key)
			}
			if ev.Severity != events.SeverityWarning {
				t.Errorf("event severity = %q, want warning", ev.Severity)
			}
		})
	}
}

// TestHandleHeartbeatRefusalLeavesLivenessAlone: a refused heartbeat must
// not be a half-write. LastHeartbeat is what the heartbeat healthcheck,
// the restart policy, and shift readiness all read.
func TestHandleHeartbeatRefusalLeavesLivenessAlone(t *testing.T) {
	d := newHandlerDaemon(t)
	key, token := registerSession(t, d, "agent-0", true)

	if resp := d.handleHeartbeat(mustMarshal(t, heartbeatParams{
		SessionKey: key, SessionToken: token, ContextPercent: 40,
	})); resp.Error != "" {
		t.Fatalf("first heartbeat: %s", resp.Error)
	}
	admitted, err := d.store.GetSession(key)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}

	if resp := d.handleHeartbeat(mustMarshal(t, heartbeatParams{
		SessionKey: key, SessionToken: "0000", ContextPercent: 99,
	})); resp.Error == "" {
		t.Fatal("a forged heartbeat was admitted")
	}

	after, err := d.store.GetSession(key)
	if err != nil {
		t.Fatalf("get session after refusal: %v", err)
	}
	if !after.LastHeartbeat.Equal(admitted.LastHeartbeat) {
		t.Errorf("refused heartbeat moved LastHeartbeat to %v, want %v",
			after.LastHeartbeat, admitted.LastHeartbeat)
	}
	if after.ContextPercent != 40 {
		t.Errorf("refused heartbeat wrote CTX%% %v, want the last admitted 40", after.ContextPercent)
	}
}

// TestSessionTokenNeverLeavesOverGet: the token binds a heartbeat only
// while sibling agents cannot read it, and they reach the same socket.
func TestSessionTokenNeverLeavesOverGet(t *testing.T) {
	d := newHandlerDaemon(t)
	_, token := registerSession(t, d, "agent-0", true)

	resp := d.handleGet(mustMarshal(t, getParams{ResourceType: "sessions"}))
	if resp.Error != "" {
		t.Fatalf("get sessions: %s", resp.Error)
	}
	if strings.Contains(string(resp.Result), token) {
		t.Fatal("a session's heartbeat token is readable over the get RPC")
	}

	resp = d.handleDescribe(mustMarshal(t, describeParams{ResourceType: "session", Name: "ws/agent-0"}))
	if resp.Error != "" {
		t.Fatalf("describe session: %s", resp.Error)
	}
	if strings.Contains(string(resp.Result), token) {
		t.Fatal("a session's heartbeat token is readable over the describe RPC")
	}
}
