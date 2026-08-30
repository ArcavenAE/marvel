package api

import "testing"

// gradedSessionKey is the fixed key every store-level heartbeat test in
// this file registers and beats against.
const gradedSessionKey = "ws/agent-0"

// gradedSession registers a bound session at gradedSessionKey carrying an
// optional manifest window, the way the manager does at spawn, and returns
// the plaintext token the way it reaches the agent's environment.
func gradedSession(t *testing.T, s *Store, manifestLimit int) (token string) {
	t.Helper()
	tok, hash, err := NewHeartbeatToken()
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	sess := &Session{
		Name:      "agent-0",
		Workspace: "ws",
		Team:      "squad",
		Role:      "worker",
		Runtime: Runtime{
			Name:          "claude",
			Command:       "claude",
			ContextWindow: manifestLimit,
		},
		State:              SessionRunning,
		HeartbeatTokenHash: hash,
	}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return tok
}

// resolverCall records what the store handed the injected resolver, so a
// test can prove the heartbeat path routes BOTH the feed window and the
// session's manifest limit through it — the difference between stamping
// the label "feed" and actually climbing the ladder (aae-orc-38yr, MUST
// NOT MISS #1).
type resolverCall struct {
	harness       string
	model         string
	args          []string
	manifestLimit int
	feedLimit     int
}

// ladderResolver is a fake standing in for usage.Resolver's feed/manifest
// rungs. The store must not embed the ladder itself (internal/api cannot
// import internal/usage), so the store's whole contribution is to CALL a
// resolver with the right inputs and stamp what it returns. This fake
// makes that contribution assertable in isolation; the daemon package
// tests the real usage.Resolver end to end.
func ladderResolver(rec *resolverCall) ContextLimitResolveFunc {
	return func(harness, model string, args []string, manifestLimit, feedLimit int) (int, string) {
		*rec = resolverCall{harness, model, args, manifestLimit, feedLimit}
		// The real ladder: an operator's manifest override outranks a
		// side-channel feed (limitLadder in internal/usage).
		if manifestLimit > 0 {
			return manifestLimit, "manifest"
		}
		if feedLimit > 0 {
			return feedLimit, "feed"
		}
		return 0, "unresolved"
	}
}

// TestHeartbeatCarriesNumeratorAndDenominator is the headline of
// aae-orc-38yr: a cooperative feed that ships the numerator (occupancy
// tokens) and the denominator (its declared window) produces a GRADED
// reading — tokens, a resolved limit, a limit source — and the percentage
// is DERIVED from the two, not taken from the wire's bare figure.
func TestHeartbeatCarriesNumeratorAndDenominator(t *testing.T) {
	t.Parallel()
	s := NewStore()
	var rec resolverCall
	s.SetContextLimitResolver(ladderResolver(&rec))

	token := gradedSession(t, s, 0) // no manifest override

	if _, err := s.UpdateSessionHeartbeat(HeartbeatRequest{
		SessionKey:     gradedSessionKey,
		SessionToken:   token,
		ContextTokens:  120_000,
		ContextWindow:  200_000,
		Model:          "claude-opus-4-8",
		ContextPercent: 60, // the harness's own figure, present as a fallback
	}); err != nil {
		t.Fatalf("update heartbeat: %v", err)
	}

	got, _ := s.GetSession(gradedSessionKey)
	if got.ContextSource != ContextSourceHeartbeat {
		t.Fatalf("ContextSource = %q, want %q", got.ContextSource, ContextSourceHeartbeat)
	}
	if got.ContextTokens != 120_000 {
		t.Fatalf("ContextTokens = %d, want 120000 (the numerator)", got.ContextTokens)
	}
	if got.ContextLimit != 200_000 {
		t.Fatalf("ContextLimit = %d, want 200000 (the resolved denominator)", got.ContextLimit)
	}
	if got.ContextLimitSource != "feed" {
		t.Fatalf("ContextLimitSource = %q, want \"feed\"", got.ContextLimitSource)
	}
	if got.ContextPercent != 60 {
		t.Fatalf("ContextPercent = %.2f, want 60 (derived 100*120000/200000)", got.ContextPercent)
	}

	// MUST NOT MISS #1: both the feed window AND the manifest limit are
	// routed through the resolver, or the operator override never gets its
	// chance to outrank the feed.
	if rec.feedLimit != 200_000 {
		t.Fatalf("resolver feedLimit = %d, want 200000 routed from the wire", rec.feedLimit)
	}
	if rec.manifestLimit != 0 {
		t.Fatalf("resolver manifestLimit = %d, want 0 read from the session record", rec.manifestLimit)
	}
	if rec.model != "claude-opus-4-8" {
		t.Fatalf("resolver model = %q, want the heartbeat's model", rec.model)
	}
}

// TestHeartbeatManifestOutranksFeed proves the ruling has EFFECT, not just
// a label: when an operator has set runtime.context_window, that window
// wins over the feed's, and the derived percentage is against the
// operator's denominator.
func TestHeartbeatManifestOutranksFeed(t *testing.T) {
	t.Parallel()
	s := NewStore()
	var rec resolverCall
	s.SetContextLimitResolver(ladderResolver(&rec))

	token := gradedSession(t, s, 500_000) // operator override

	if _, err := s.UpdateSessionHeartbeat(HeartbeatRequest{
		SessionKey:    gradedSessionKey,
		SessionToken:  token,
		ContextTokens: 120_000,
		ContextWindow: 200_000, // the feed's window, which the manifest outranks
		Model:         "claude-opus-4-8",
	}); err != nil {
		t.Fatalf("update heartbeat: %v", err)
	}

	got, _ := s.GetSession(gradedSessionKey)
	if got.ContextLimit != 500_000 {
		t.Fatalf("ContextLimit = %d, want 500000 (manifest outranks feed)", got.ContextLimit)
	}
	if got.ContextLimitSource != "manifest" {
		t.Fatalf("ContextLimitSource = %q, want \"manifest\"", got.ContextLimitSource)
	}
	if got.ContextPercent != 24 {
		t.Fatalf("ContextPercent = %.2f, want 24 (100*120000/500000, against the operator's window)", got.ContextPercent)
	}
	if rec.manifestLimit != 500_000 || rec.feedLimit != 200_000 {
		t.Fatalf("resolver got manifest=%d feed=%d, want both routed (500000, 200000)", rec.manifestLimit, rec.feedLimit)
	}
}

// TestHeartbeatWithoutNumeratorStaysPercentageOnly is the graceful-
// degradation half: a producer that ships only a percentage (the
// simulator, an older harness) keeps the exact behavior it had — a
// percentage-only reading, no window, no token count — so widening the
// contract breaks no existing producer.
func TestHeartbeatWithoutNumeratorStaysPercentageOnly(t *testing.T) {
	t.Parallel()
	s := NewStore()
	var rec resolverCall
	s.SetContextLimitResolver(ladderResolver(&rec))

	token := gradedSession(t, s, 0)

	if _, err := s.UpdateSessionHeartbeat(HeartbeatRequest{
		SessionKey:     gradedSessionKey,
		SessionToken:   token,
		ContextPercent: 40,
	}); err != nil {
		t.Fatalf("update heartbeat: %v", err)
	}

	got, _ := s.GetSession(gradedSessionKey)
	if got.ContextSource != ContextSourceHeartbeat {
		t.Fatalf("ContextSource = %q, want %q", got.ContextSource, ContextSourceHeartbeat)
	}
	if got.ContextPercent != 40 {
		t.Fatalf("ContextPercent = %.2f, want 40 (the agent's own figure)", got.ContextPercent)
	}
	if got.ContextLimit != 0 || got.ContextTokens != 0 || got.ContextLimitSource != "" {
		t.Fatalf("percentage-only reading leaked a window: limit=%d tokens=%d src=%q",
			got.ContextLimit, got.ContextTokens, got.ContextLimitSource)
	}
	if rec.harness != "" || rec.model != "" || rec.manifestLimit != 0 || rec.feedLimit != 0 {
		t.Fatalf("resolver was called for a windowless heartbeat: %+v", rec)
	}
}

// TestHeartbeatDerivesPercentFromNumDenomNotWirePercent is the "consumers
// derive the percentage" guarantee: when the store can compute occupancy
// against the denominator it trusts, it does — and ignores a bare
// percentage that disagrees. A harness percentage is against the harness's
// own window; once marvel resolves its own denominator, its own division
// is the honest reading.
func TestHeartbeatDerivesPercentFromNumDenomNotWirePercent(t *testing.T) {
	t.Parallel()
	s := NewStore()
	var rec resolverCall
	s.SetContextLimitResolver(ladderResolver(&rec))

	token := gradedSession(t, s, 0)

	if _, err := s.UpdateSessionHeartbeat(HeartbeatRequest{
		SessionKey:     gradedSessionKey,
		SessionToken:   token,
		ContextTokens:  100_000,
		ContextWindow:  200_000,
		ContextPercent: 99, // a bare percentage that contradicts 100000/200000
	}); err != nil {
		t.Fatalf("update heartbeat: %v", err)
	}

	got, _ := s.GetSession(gradedSessionKey)
	if got.ContextPercent != 50 {
		t.Fatalf("ContextPercent = %.2f, want 50 (derived), not 99 (the bare wire figure)", got.ContextPercent)
	}
}

// TestHeartbeatWithoutResolverStaysPercentageOnly guards the nil-resolver
// path: a store built without a resolver (every store outside the daemon,
// including tests) must not panic and must degrade to a percentage-only
// reading rather than pretending to a window it cannot resolve.
func TestHeartbeatWithoutResolverStaysPercentageOnly(t *testing.T) {
	t.Parallel()
	s := NewStore() // no SetContextLimitResolver

	token := gradedSession(t, s, 0)

	if _, err := s.UpdateSessionHeartbeat(HeartbeatRequest{
		SessionKey:     gradedSessionKey,
		SessionToken:   token,
		ContextTokens:  120_000,
		ContextWindow:  200_000,
		ContextPercent: 60,
	}); err != nil {
		t.Fatalf("update heartbeat: %v", err)
	}

	got, _ := s.GetSession(gradedSessionKey)
	if got.ContextLimit != 0 {
		t.Fatalf("ContextLimit = %d, want 0 with no resolver wired", got.ContextLimit)
	}
	if got.ContextPercent != 60 {
		t.Fatalf("ContextPercent = %.2f, want 60 (the wire figure stands)", got.ContextPercent)
	}
	// The numerator is still recorded even when no window resolves: it is a
	// real measurement, and ContextLimit==0 beside it is the documented
	// "tokens measured, window unknown" state.
	if got.ContextTokens != 120_000 {
		t.Fatalf("ContextTokens = %d, want 120000 even with no resolver", got.ContextTokens)
	}
}

// TestHeartbeatNumeratorWithUnresolvedWindow is the "tokens measured,
// window unknown" branch: a numerator arrives but no rung resolves a
// window (no feed window, no manifest, nothing learned). The reading must
// record the numerator, leave ContextLimit 0 and ContextLimitSource empty,
// and keep the agent's own percentage — the shape SessionContext documents
// as the third state.
func TestHeartbeatNumeratorWithUnresolvedWindow(t *testing.T) {
	t.Parallel()
	s := NewStore()
	var rec resolverCall
	s.SetContextLimitResolver(ladderResolver(&rec))

	token := gradedSession(t, s, 0) // no manifest override

	if _, err := s.UpdateSessionHeartbeat(HeartbeatRequest{
		SessionKey:     gradedSessionKey,
		SessionToken:   token,
		ContextTokens:  90_000,
		ContextWindow:  0, // the feed declared no window
		ContextPercent: 45,
	}); err != nil {
		t.Fatalf("update heartbeat: %v", err)
	}

	got, _ := s.GetSession(gradedSessionKey)
	if got.ContextTokens != 90_000 {
		t.Fatalf("ContextTokens = %d, want 90000 (measured)", got.ContextTokens)
	}
	if got.ContextLimit != 0 || got.ContextLimitSource != "" {
		t.Fatalf("limit=%d src=%q, want 0 / \"\" (window unknown)", got.ContextLimit, got.ContextLimitSource)
	}
	if got.ContextPercent != 45 {
		t.Fatalf("ContextPercent = %.2f, want 45 (the agent's own figure stands)", got.ContextPercent)
	}
}

// TestHeartbeatDerivedPercentIsNotClamped pins the deliberate decision
// flagged in review: the derived percentage is NOT capped at 100, matching
// the accountant's own unclamped derive. When occupancy exceeds the
// resolved window — an operator's manifest override smaller than the window
// the harness measured against — CTX% must read "over budget", not "at the
// limit". This is a contract choice, held by a test so it cannot regress
// into an accidental clamp or an accidental symmetry break with the
// accountant.
func TestHeartbeatDerivedPercentIsNotClamped(t *testing.T) {
	t.Parallel()
	s := NewStore()
	var rec resolverCall
	s.SetContextLimitResolver(ladderResolver(&rec))

	token := gradedSession(t, s, 100_000) // operator caps the window below occupancy

	if _, err := s.UpdateSessionHeartbeat(HeartbeatRequest{
		SessionKey:    gradedSessionKey,
		SessionToken:  token,
		ContextTokens: 120_000, // 20% over the operator's declared window
		ContextWindow: 200_000,
		Model:         "claude-opus-4-8",
	}); err != nil {
		t.Fatalf("update heartbeat: %v", err)
	}

	got, _ := s.GetSession(gradedSessionKey)
	if got.ContextLimit != 100_000 || got.ContextLimitSource != "manifest" {
		t.Fatalf("limit=%d src=%q, want 100000 / manifest", got.ContextLimit, got.ContextLimitSource)
	}
	if got.ContextPercent != 120 {
		t.Fatalf("ContextPercent = %.2f, want 120 (unclamped, over the operator's budget)", got.ContextPercent)
	}
}
