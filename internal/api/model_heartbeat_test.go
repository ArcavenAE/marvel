package api

import "testing"

// The heartbeat path resolves a window the way the accountant does, so it is
// handed the session's declared model, and it keeps the id the harness
// reported beside the display name.

func modelSession(t *testing.T, s *Store, declared string) (token string) {
	t.Helper()
	tok, hash, err := NewHeartbeatToken()
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	sess := &Session{
		Name: "agent-0", Workspace: "ws", Team: "squad", Role: "worker",
		Runtime:            Runtime{Name: "claude", Command: "claude", Model: declared},
		State:              SessionRunning,
		HeartbeatTokenHash: hash,
	}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return tok
}

func TestHeartbeatResolverReceivesDeclaredModel(t *testing.T) {
	t.Parallel()
	s := NewStore()
	var got string
	s.SetContextLimitResolver(func(harness, model, declaredModel string, args []string, env map[string]string, manifestLimit, feedLimit int, redirection BackendRedirection) (int, string) {
		got = declaredModel
		return 0, "unresolved"
	})
	token := modelSession(t, s, "claude-sonnet-5-5")
	if _, err := s.UpdateSessionHeartbeat(HeartbeatRequest{
		SessionKey: gradedSessionKey, SessionToken: token, ContextTokens: 120_000, Model: "Sonnet 5.5",
	}); err != nil {
		t.Fatalf("update heartbeat: %v", err)
	}
	if got != "claude-sonnet-5-5" {
		t.Errorf("the resolver was handed declared model %q, want claude-sonnet-5-5", got)
	}
}

func TestHeartbeatStoresObservedModelID(t *testing.T) {
	t.Parallel()
	s := NewStore()
	token := modelSession(t, s, "")
	if _, err := s.UpdateSessionHeartbeat(HeartbeatRequest{
		SessionKey: gradedSessionKey, SessionToken: token, ContextPercent: 17,
		Model: "Opus 5 (1M context)", ModelID: "claude-opus-5[1m]",
	}); err != nil {
		t.Fatalf("update heartbeat: %v", err)
	}
	got, _ := s.GetSession(gradedSessionKey)
	if got.ObservedModelID != "claude-opus-5[1m]" {
		t.Errorf("ObservedModelID = %q, want the id the harness reported", got.ObservedModelID)
	}
	if got.ContextModel != "Opus 5 (1M context)" {
		t.Errorf("ContextModel = %q, want the display name kept", got.ContextModel)
	}
}
