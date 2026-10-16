package api

import (
	"reflect"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/asof"
)

// TestInteractiveSeatSpendAbsent: a seat that reports only a heartbeat has
// no token stream, so its spend is absent and never zero. A heartbeat carries
// no spend, so it does not invent one (aae-orc-ibu9, marvel#779).
func TestInteractiveSeatSpendAbsent(t *testing.T) {
	t.Parallel()
	s := NewStore()
	token := gradedSession(t, s, 0)

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
	if !reflect.DeepEqual(got.OutRate, asof.Cell[float64]{}) {
		t.Errorf("heartbeat seat carries an out rate %+v, want none", got.OutRate)
	}
}

// TestMixedSeatKeepsSpendAcrossHeartbeat: a seat with a token stream and a
// statusline feed gets spend from the accountant and occupancy from the
// heartbeat. A heartbeat replaces the occupancy reading, which is a complete
// reading of its own shape, and leaves the accountant-owned spend alone, so the
// TOUT and PROMPT cells do not flip to "-" on every beat (marvel#779).
func TestMixedSeatKeepsSpendAcrossHeartbeat(t *testing.T) {
	t.Parallel()
	s := NewStore()
	token := gradedSession(t, s, 0)

	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	out, prompt := 120, 4500
	rate := asof.Cell[float64]{Value: 3.5, ObservedAt: at, ValidUntil: at.Add(time.Minute)}
	s.UpdateSessionContext(gradedSessionKey, SessionContext{
		ContextSource:     ContextSourceAccountant,
		ContextTokens:     1000,
		SpendOut:          &out,
		SpendPromptTokens: &prompt,
		OutRate:           rate,
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
	if got.SpendOut == nil || *got.SpendOut != out || got.SpendPromptTokens == nil || *got.SpendPromptTokens != prompt {
		t.Errorf("mixed seat spend out=%v prompt=%v after a heartbeat, want %d and %d kept", got.SpendOut, got.SpendPromptTokens, out, prompt)
	}
	if !reflect.DeepEqual(got.OutRate, rate) {
		t.Errorf("mixed seat out rate = %+v after a heartbeat, want %+v kept", got.OutRate, rate)
	}
	// The occupancy is the heartbeat's own: it does not inherit accountant
	// provenance (aae-orc-ibu9).
	if got.ContextSource != ContextSourceHeartbeat {
		t.Errorf("ContextSource = %q after a heartbeat, want %q", got.ContextSource, ContextSourceHeartbeat)
	}
}
