package api

import "testing"

// beat sends one statusline-shaped heartbeat carrying an occupancy reading.
func beat(t *testing.T, s *Store, token string, tokens int) Session {
	t.Helper()
	if _, err := s.UpdateSessionHeartbeat(HeartbeatRequest{
		SessionKey: gradedSessionKey, SessionToken: token,
		ContextTokens: tokens, ContextWindow: 1_000_000, Model: "claude-opus-4-8",
	}); err != nil {
		t.Fatalf("heartbeat at %d tokens: %v", tokens, err)
	}
	got, err := s.GetSession(gradedSessionKey)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// A statusline-fed seat that compacts reads a sharp drop between two
// heartbeats on the same record. That drop is counted, as the accountant counts
// it on the request path (marvel#630).
func TestHeartbeatCountsASharpDropAsACompaction(t *testing.T) {
	t.Parallel()
	s := NewStore()
	s.SetContextLimitResolver(ladderResolver(&resolverCall{}))
	token := gradedSession(t, s, 0)

	beat(t, s, token, 250_000)
	if got := beat(t, s, token, 260_000); got.ContextCompactions != 0 {
		t.Fatalf("growth counted: ContextCompactions = %d, want 0", got.ContextCompactions)
	}
	if got := beat(t, s, token, 20_000); got.ContextCompactions != 1 {
		t.Fatalf("ContextCompactions = %d after 260000 -> 20000, want 1", got.ContextCompactions)
	}
}

// The count is part of what the next heartbeat carries forward. Each heartbeat
// replaces the reading, so a count that is not carried would read 0 again one
// beat after the compaction.
func TestHeartbeatKeepsTheCompactionCountAcrossLaterBeats(t *testing.T) {
	t.Parallel()
	s := NewStore()
	s.SetContextLimitResolver(ladderResolver(&resolverCall{}))
	token := gradedSession(t, s, 0)

	beat(t, s, token, 300_000)
	beat(t, s, token, 10_000)
	if got := beat(t, s, token, 12_000); got.ContextCompactions != 1 {
		t.Fatalf("ContextCompactions = %d one beat later, want 1", got.ContextCompactions)
	}
	beat(t, s, token, 200_000)
	if got := beat(t, s, token, 5_000); got.ContextCompactions != 2 {
		t.Fatalf("ContextCompactions = %d after a second compaction, want 2", got.ContextCompactions)
	}
}

// A drop inside the hysteresis band is noise, not a compaction: the larger of
// 2048 tokens and 10% of the prior reading.
func TestHeartbeatIgnoresADropInsideTheHysteresisBand(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		from, to   int
		compaction int
	}{
		{"inside the fractional band", 300_000, 271_000, 0},
		{"exactly the fractional band", 300_000, 270_000, 0},
		{"just past the fractional band", 300_000, 269_000, 1},
		{"inside the absolute band", 10_000, 8_500, 0},
		{"just past the absolute band", 10_000, 7_900, 1},
		{"to zero", 40_000, 0, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := NewStore()
			s.SetContextLimitResolver(ladderResolver(&resolverCall{}))
			token := gradedSession(t, s, 0)
			beat(t, s, token, tc.from)
			if got := beat(t, s, token, tc.to); got.ContextCompactions != tc.compaction {
				t.Fatalf("%d -> %d: ContextCompactions = %d, want %d", tc.from, tc.to, got.ContextCompactions, tc.compaction)
			}
		})
	}
}

// A first reading has nothing to drop from, and a feed that ships no
// numerator never has a prior occupancy to compare with.
func TestHeartbeatCountsNoCompactionWithoutAPriorOccupancy(t *testing.T) {
	t.Parallel()
	s := NewStore()
	token := gradedSession(t, s, 0)
	if got := beat(t, s, token, 400_000); got.ContextCompactions != 0 {
		t.Fatalf("first reading counted: %d", got.ContextCompactions)
	}

	s2 := NewStore()
	token2 := gradedSession(t, s2, 0)
	for _, pct := range []float64{80, 10} {
		if _, err := s2.UpdateSessionHeartbeat(HeartbeatRequest{SessionKey: gradedSessionKey, SessionToken: token2, ContextPercent: pct}); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s2.GetSession(gradedSessionKey)
	if got.ContextCompactions != 0 {
		t.Fatalf("a percentage-only feed counted %d compactions, want 0", got.ContextCompactions)
	}
}
