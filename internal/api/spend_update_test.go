package api

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/asof"
)

// The spend-only setter writes the spend and the rate and leaves everything
// else the session's context reading says untouched: the occupancy, who
// produced it, and ContextAt, which the watchdog reads as "the seat did work".
func TestSpendUpdateLeavesContextUntouched(t *testing.T) {
	t.Parallel()
	s := NewStore()
	sess := &Session{Name: "a", Workspace: "ws", Team: "t", Role: "r", State: SessionRunning}
	if err := s.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	if err := s.UpdateSession(sess.Key(), func(live *Session) error {
		live.SessionContext = SessionContext{
			ContextSource: ContextSourceHeartbeat, ContextPercent: 42, ContextTokens: 420,
			ContextLimit: 1000, ContextAt: at,
		}
		live.LastHeartbeat = at
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	out, prompt := 76, 28110
	rate := asof.Cell[float64]{Value: 3.5, ObservedAt: at, ValidUntil: at.Add(time.Minute)}
	s.UpdateSessionSpend(sess.Key(), SessionSpend{Out: &out, PromptTokens: &prompt, OutRate: rate})

	got, _ := s.GetSession(sess.Key())
	if got.SpendOut == nil || *got.SpendOut != 76 || got.SpendPromptTokens == nil || *got.SpendPromptTokens != 28110 {
		t.Fatalf("spend = %v / %v, want 76 / 28110", got.SpendOut, got.SpendPromptTokens)
	}
	if got.OutRate.Value != 3.5 || !got.OutRate.ObservedAt.Equal(at) {
		t.Errorf("OutRate = %+v, want the cell written", got.OutRate)
	}
	if got.ContextSource != ContextSourceHeartbeat || got.ContextPercent != 42 || got.ContextTokens != 420 || got.ContextLimit != 1000 {
		t.Errorf("occupancy fields changed: %+v", got.SessionContext)
	}
	if !got.ContextAt.Equal(at) {
		t.Errorf("ContextAt = %s, want it unchanged at %s: the spend write is not activity", got.ContextAt, at)
	}
	if !got.LastHeartbeat.Equal(at) {
		t.Errorf("LastHeartbeat moved to %s", got.LastHeartbeat)
	}
}

// A missing session is ignored, and the written pointers are the store's own:
// a later edit to the caller's ints does not reach the stored spend.
func TestSpendUpdateIgnoresAMissingSessionAndCopiesTheValues(t *testing.T) {
	t.Parallel()
	s := NewStore()
	out := 1
	s.UpdateSessionSpend("ws/ghost", SessionSpend{Out: &out}) // must not panic

	sess := &Session{Name: "a", Workspace: "ws", Team: "t", Role: "r", State: SessionRunning}
	if err := s.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	prompt := 2
	s.UpdateSessionSpend(sess.Key(), SessionSpend{Out: &out, PromptTokens: &prompt})
	out, prompt = 99, 99

	got, _ := s.GetSession(sess.Key())
	if got.SpendOut == nil || *got.SpendOut != 1 || got.SpendPromptTokens == nil || *got.SpendPromptTokens != 2 {
		t.Fatalf("stored spend followed the caller's variables: %v / %v", got.SpendOut, got.SpendPromptTokens)
	}
}
