package api

import "testing"

// TestInteractiveSeatSpendAbsent: a seat that reports only a heartbeat has
// no token stream, so its spend is absent and never zero. A heartbeat that
// lands after an accountant reading replaces the whole record, spend
// included, the way it already replaces CTX provenance (aae-orc-ibu9).
func TestInteractiveSeatSpendAbsent(t *testing.T) {
	t.Parallel()
	s := NewStore()
	token := gradedSession(t, s, 0)

	out, prompt := 120, 4500
	s.UpdateSessionContext(gradedSessionKey, SessionContext{
		ContextSource:     ContextSourceAccountant,
		ContextTokens:     1000,
		SpendOut:          &out,
		SpendPromptTokens: &prompt,
	})
	if got, _ := s.GetSession(gradedSessionKey); got.SpendOut == nil {
		t.Fatal("setup: the accountant reading should carry spend")
	}

	if _, err := s.UpdateSessionHeartbeat(HeartbeatRequest{
		SessionKey:     gradedSessionKey,
		SessionToken:   token,
		ContextPercent: 40,
	}); err != nil {
		t.Fatalf("update heartbeat: %v", err)
	}

	got, _ := s.GetSession(gradedSessionKey)
	if got.SpendOut != nil || got.SpendPromptTokens != nil {
		t.Errorf("heartbeat seat carries spend out=%v prompt=%v, want both absent (never a convincing 0)",
			got.SpendOut, got.SpendPromptTokens)
	}
}
