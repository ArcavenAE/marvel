package api

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/asof"
)

// reloadSession writes a session's context and spend in the given order,
// persists it, closes the store and opens it again, as a daemon restart does,
// and returns the session as the new store holds it.
func reloadSession(t *testing.T, src ContextSourceKind, requests int, spendFirst bool) Session {
	t.Helper()
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	s1 := NewStore()
	if err := s1.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #1: %v", err)
	}
	sess := &Session{Name: "agent-0", Workspace: "ws", Team: "team", Role: "worker", State: SessionRunning, PaneID: "%1"}
	if err := s1.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	key := sess.Key()
	out, prompt := 48200, 9000
	spend := SessionSpend{
		Out: &out, PromptTokens: &prompt,
		OutRate: asof.Cell[float64]{Value: 12.5, ObservedAt: time.Now().UTC(), ValidUntil: time.Now().UTC().Add(10 * time.Minute)},
	}
	context := SessionContext{ContextSource: src, ContextPercent: 40, ContextRequests: requests}
	if spendFirst {
		s1.UpdateSessionSpend(key, spend)
		s1.UpdateSessionContext(key, context)
	} else {
		s1.UpdateSessionContext(key, context)
		s1.UpdateSessionSpend(key, spend)
	}
	if err := s1.UpdateSession(key, func(*Session) error { return nil }); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	if err := s1.CloseBolt(); err != nil {
		t.Fatalf("CloseBolt: %v", err)
	}
	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #2: %v", err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	got, err := s2.GetSession(key)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	return got
}

// No session carries a spend across a restart. Nothing observes the stream of
// an adopted session, so a spend that survived the load would never move
// again, and a figure that cannot move is not a lower bound either
// (aae-orc-88bm0). The spend slice is the same whichever producer wrote the
// context and in whichever order the two writes landed.
func TestBoltLoadDropsTheSpendForEverySource(t *testing.T) {
	for _, tc := range []struct {
		name     string
		src      ContextSourceKind
		requests int
	}{
		{"heartbeat", ContextSourceHeartbeat, 0},
		{"accountant", ContextSourceAccountant, 7},
		{"legacy with a request count", ContextSourceNone, 7},
		{"legacy without one", ContextSourceNone, 0},
	} {
		for _, spendFirst := range []bool{false, true} {
			got := reloadSession(t, tc.src, tc.requests, spendFirst)
			if got.SpendOut != nil || got.SpendPromptTokens != nil {
				t.Errorf("%s (spend first=%v): SpendOut=%v SpendPromptTokens=%v after reload, want both nil",
					tc.name, spendFirst, got.SpendOut, got.SpendPromptTokens)
			}
			if !got.OutRate.ObservedAt.IsZero() || got.OutRate.Value != 0 {
				t.Errorf("%s (spend first=%v): OutRate=%+v after reload, want the zero cell", tc.name, spendFirst, got.OutRate)
			}
		}
	}
}

// A heartbeat reading is refreshed by the agent itself, so its occupancy
// survives the load; only the spend slice goes.
func TestBoltLoadKeepsAHeartbeatOccupancy(t *testing.T) {
	got := reloadSession(t, ContextSourceHeartbeat, 0, false)
	if got.ContextSource != ContextSourceHeartbeat || got.ContextPercent != 40 {
		t.Errorf("heartbeat reading after reload = %q %.0f%%, want heartbeat 40%%", got.ContextSource, got.ContextPercent)
	}
}
