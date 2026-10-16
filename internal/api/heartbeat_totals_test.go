package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/asof"
)

// The statusline feed reports the harness's running cost and API-time totals.
// A heartbeat stores them and stamps when either last moved, so a later reader
// can tell a seat that is spending from one that is only alive (marvel#801).
// Nothing reads them yet; CTX%, ContextAt and LastHeartbeat are unchanged.

func f64(v float64) *float64 { return &v }
func i64(v int64) *int64     { return &v }

func beatTotals(t *testing.T, s *Store, token string, r HeartbeatRequest) Session {
	t.Helper()
	r.SessionKey, r.SessionToken = gradedSessionKey, token
	if _, err := s.UpdateSessionHeartbeat(r); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	got, err := s.GetSession(gradedSessionKey)
	if err != nil {
		t.Fatalf("session after a heartbeat: %v", err)
	}
	return got
}

// Totals arrive on the beat and are stored on the session, and the request
// carries them over the wire the daemon decodes.
func TestHeartbeatStoresTheTotals(t *testing.T) {
	t.Parallel()
	s := NewStore()
	token := gradedSession(t, s, 0)

	got := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 10, CostTotalUSD: f64(1.25), APIDurationMS: i64(42000)})
	if got.CostTotalUSD == nil || *got.CostTotalUSD != 1.25 || got.APIDurationMS == nil || *got.APIDurationMS != 42000 {
		t.Fatalf("totals = %v and %v, want 1.25 and 42000", got.CostTotalUSD, got.APIDurationMS)
	}
	if got.TotalsChangedAt.IsZero() {
		t.Error("TotalsChangedAt is zero after the totals first arrived, want a stamp")
	}

	wire, err := json.Marshal(HeartbeatRequest{CostTotalUSD: f64(1.25), APIDurationMS: i64(42000)})
	if err != nil {
		t.Fatal(err)
	}
	var back HeartbeatRequest
	if err := json.Unmarshal(wire, &back); err != nil {
		t.Fatal(err)
	}
	if back.CostTotalUSD == nil || *back.CostTotalUSD != 1.25 || back.APIDurationMS == nil || *back.APIDurationMS != 42000 {
		t.Errorf("round trip lost the totals: %s", wire)
	}
}

// The stamp moves when either total moves and holds on an unchanged beat.
func TestTotalsStampMovesOnAChangeAndHoldsOtherwise(t *testing.T) {
	t.Parallel()
	s := NewStore()
	token := gradedSession(t, s, 0)

	first := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 10, CostTotalUSD: f64(1), APIDurationMS: i64(1000)})
	time.Sleep(2 * time.Millisecond)

	same := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 11, CostTotalUSD: f64(1), APIDurationMS: i64(1000)})
	if !same.TotalsChangedAt.Equal(first.TotalsChangedAt) {
		t.Errorf("an unchanged beat moved the stamp from %v to %v", first.TotalsChangedAt, same.TotalsChangedAt)
	}
	if !same.LastHeartbeat.After(first.LastHeartbeat) {
		t.Error("setup: the unchanged beat should still advance LastHeartbeat")
	}
	time.Sleep(2 * time.Millisecond)

	cost := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 12, CostTotalUSD: f64(1.5), APIDurationMS: i64(1000)})
	if !cost.TotalsChangedAt.After(same.TotalsChangedAt) {
		t.Errorf("a cost change left the stamp at %v", cost.TotalsChangedAt)
	}
	time.Sleep(2 * time.Millisecond)

	ms := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 13, CostTotalUSD: f64(1.5), APIDurationMS: i64(2500)})
	if !ms.TotalsChangedAt.After(cost.TotalsChangedAt) {
		t.Errorf("an API-time change left the stamp at %v", ms.TotalsChangedAt)
	}
}

// A beat where context drops is a compaction, which is an API call but not the
// seat working, so it stores the new totals and does not stamp.
func TestTotalsStampSkipsACompactionBeat(t *testing.T) {
	t.Parallel()
	s := NewStore()
	token := gradedSession(t, s, 0)

	first := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 60, ContextTokens: 600_000, CostTotalUSD: f64(5), APIDurationMS: i64(90_000)})
	time.Sleep(2 * time.Millisecond)

	compacted := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 8, ContextTokens: 80_000, CostTotalUSD: f64(5.4), APIDurationMS: i64(97_000)})
	if compacted.CostTotalUSD == nil || *compacted.CostTotalUSD != 5.4 || *compacted.APIDurationMS != 97_000 {
		t.Errorf("a compaction beat stored cost %v and API time %v, want 5.4 and 97000", compacted.CostTotalUSD, compacted.APIDurationMS)
	}
	if !compacted.TotalsChangedAt.Equal(first.TotalsChangedAt) {
		t.Errorf("a compaction beat moved the stamp from %v to %v", first.TotalsChangedAt, compacted.TotalsChangedAt)
	}
}

// A later heartbeat that carries no totals keeps both, and a heartbeat that
// never carried any leaves them unset, never a zero.
func TestTotalsSurviveATotalsFreeBeatAndNeverDefaultToZero(t *testing.T) {
	t.Parallel()
	s := NewStore()
	token := gradedSession(t, s, 0)

	bare := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 5})
	if bare.CostTotalUSD != nil || bare.APIDurationMS != nil || !bare.TotalsChangedAt.IsZero() {
		t.Fatalf("a totals-free seat holds cost %v, API time %v, stamp %v, want all unset", bare.CostTotalUSD, bare.APIDurationMS, bare.TotalsChangedAt)
	}

	with := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 6, CostTotalUSD: f64(2), APIDurationMS: i64(3000)})
	after := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 7})
	if after.CostTotalUSD == nil || *after.CostTotalUSD != 2 || after.APIDurationMS == nil || *after.APIDurationMS != 3000 {
		t.Errorf("a totals-free beat left cost %v and API time %v, want 2 and 3000 kept", after.CostTotalUSD, after.APIDurationMS)
	}
	if !after.TotalsChangedAt.Equal(with.TotalsChangedAt) {
		t.Errorf("a totals-free beat moved the stamp from %v to %v", with.TotalsChangedAt, after.TotalsChangedAt)
	}
}

// A declared zero is a value: a fresh session's cost really is 0.00, and it is
// not the same as a payload that said nothing.
func TestADeclaredZeroTotalIsStored(t *testing.T) {
	t.Parallel()
	s := NewStore()
	token := gradedSession(t, s, 0)

	got := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 1, CostTotalUSD: f64(0), APIDurationMS: i64(0)})
	if got.CostTotalUSD == nil || got.APIDurationMS == nil {
		t.Fatalf("a declared zero was dropped: cost %v, API time %v", got.CostTotalUSD, got.APIDurationMS)
	}
}

// A mixed seat has a token stream and a statusline feed, so the accountant can
// be the last writer before a beat. The stamp skips a compaction whoever wrote
// the prior reading. The compaction COUNT is a different rule and does not
// change: a drop from another producer's reading is not counted (marvel#630).
func TestTotalsStampSkipsACompactionAfterAnAccountantWrite(t *testing.T) {
	t.Parallel()
	s := NewStore()
	token := gradedSession(t, s, 0)

	first := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 60, ContextTokens: 600_000, CostTotalUSD: f64(5), APIDurationMS: i64(90_000)})
	s.UpdateSessionContext(gradedSessionKey, SessionContext{ContextSource: ContextSourceAccountant, ContextTokens: 600_000})
	time.Sleep(2 * time.Millisecond)

	compacted := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 8, ContextTokens: 80_000, CostTotalUSD: f64(5.4), APIDurationMS: i64(97_000)})
	if compacted.CostTotalUSD == nil || *compacted.CostTotalUSD != 5.4 {
		t.Errorf("cost = %v after the compaction beat, want 5.4 stored", compacted.CostTotalUSD)
	}
	if !compacted.TotalsChangedAt.Equal(first.TotalsChangedAt) {
		t.Errorf("a compaction after an accountant write moved the stamp from %v to %v", first.TotalsChangedAt, compacted.TotalsChangedAt)
	}
	if compacted.ContextCompactions != 0 {
		t.Errorf("ContextCompactions = %d, want 0: a drop from another producer's reading is not counted", compacted.ContextCompactions)
	}
}

// A beat that carries no numerator cannot show a drop, so it does not read as a
// compaction: totals that move on it stamp.
func TestTotalsStampMovesOnABeatWithNoNumerator(t *testing.T) {
	t.Parallel()
	s := NewStore()
	token := gradedSession(t, s, 0)

	first := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 60, ContextTokens: 600_000, CostTotalUSD: f64(5), APIDurationMS: i64(90_000)})
	time.Sleep(2 * time.Millisecond)

	bare := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 61, CostTotalUSD: f64(5.2), APIDurationMS: i64(91_000)})
	if !bare.TotalsChangedAt.After(first.TotalsChangedAt) {
		t.Errorf("a percentage-only beat with moved totals left the stamp at %v", bare.TotalsChangedAt)
	}
}

// A total that goes down is a move too, as when the harness starts a fresh
// session under the same seat: it stamps, and the lower value replaces the old.
func TestADroppingTotalStampsAndKeepsTheLowerValue(t *testing.T) {
	t.Parallel()
	s := NewStore()
	token := gradedSession(t, s, 0)

	first := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 10, CostTotalUSD: f64(9), APIDurationMS: i64(500_000)})
	time.Sleep(2 * time.Millisecond)

	lower := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 11, CostTotalUSD: f64(0.4), APIDurationMS: i64(12_000)})
	if lower.CostTotalUSD == nil || *lower.CostTotalUSD != 0.4 || lower.APIDurationMS == nil || *lower.APIDurationMS != 12_000 {
		t.Errorf("totals = %v and %v after a drop, want 0.4 and 12000 kept", lower.CostTotalUSD, lower.APIDurationMS)
	}
	if !lower.TotalsChangedAt.After(first.TotalsChangedAt) {
		t.Errorf("a dropping total left the stamp at %v", lower.TotalsChangedAt)
	}
}

func intPtr(v int) *int { return &v }

// A codex hook beat can carry the rollout's output total. It lands on the
// session's SpendOut, which is what the TOUT cell reads (marvel#927).
func TestHeartbeatStoresTheOutputTotal(t *testing.T) {
	t.Parallel()
	s := NewStore()
	token := gradedSession(t, s, 0)

	got := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 10, OutputTokens: intPtr(4321)})
	if got.SpendOut == nil || *got.SpendOut != 4321 {
		t.Fatalf("SpendOut = %v, want 4321", got.SpendOut)
	}

	wire, err := json.Marshal(HeartbeatRequest{OutputTokens: intPtr(4321)})
	if err != nil {
		t.Fatal(err)
	}
	var back HeartbeatRequest
	if err := json.Unmarshal(wire, &back); err != nil {
		t.Fatal(err)
	}
	if back.OutputTokens == nil || *back.OutputTokens != 4321 {
		t.Errorf("round trip lost the output total: %s", wire)
	}
}

// A beat that carries no output total keeps what the session holds, and one
// never carried stays nil and never reads as zero.
func TestOutputTotalKeptAcrossBeatsAndNeverZero(t *testing.T) {
	t.Parallel()
	s := NewStore()
	token := gradedSession(t, s, 0)

	none := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 10})
	if none.SpendOut != nil {
		t.Fatalf("SpendOut = %d after a beat with no total, want nil", *none.SpendOut)
	}

	beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 11, OutputTokens: intPtr(500)})
	later := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 12})
	if later.SpendOut == nil || *later.SpendOut != 500 {
		t.Errorf("SpendOut = %v after a beat with no total, want the 500 already held", later.SpendOut)
	}

	up := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 13, OutputTokens: intPtr(900)})
	if up.SpendOut == nil || *up.SpendOut != 900 {
		t.Errorf("SpendOut = %v, want the newer total 900", up.SpendOut)
	}
}

// The total is copied in, so a caller that reuses its pointer cannot move the
// stored value afterwards.
func TestOutputTotalIsCopiedIn(t *testing.T) {
	t.Parallel()
	s := NewStore()
	token := gradedSession(t, s, 0)

	v := 700
	beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 10, OutputTokens: &v})
	v = 1
	got, err := s.GetSession(gradedSessionKey)
	if err != nil {
		t.Fatal(err)
	}
	if got.SpendOut == nil || *got.SpendOut != 700 {
		t.Errorf("SpendOut = %v after the caller changed its value, want 700", got.SpendOut)
	}
}

// A seat the accountant has metered keeps the accountant's output total, its
// prompt total and the rate computed over them, whatever a codex hook beat
// carries. The sequence is the reviewer's: accountant 120/4500, hook 500,
// accountant 130, hook 500. The slice is never mixed, so OutRate always
// describes the SpendOut on show (marvel#934).
func TestHookBeatDoesNotOverwriteAccountantSpend(t *testing.T) {
	t.Parallel()
	s := NewStore()
	token := gradedSession(t, s, 0)

	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	rate := asof.Cell[float64]{Value: 3.5, ObservedAt: at, ValidUntil: at.Add(time.Minute)}
	meter := func(out, prompt int) {
		s.UpdateSessionSpend(gradedSessionKey, SessionSpend{Out: &out, PromptTokens: &prompt, OutRate: rate})
	}
	check := func(step string, wantOut, wantPrompt int) {
		t.Helper()
		got, err := s.GetSession(gradedSessionKey)
		if err != nil {
			t.Fatal(err)
		}
		if got.SpendOut == nil || *got.SpendOut != wantOut || got.SpendPromptTokens == nil || *got.SpendPromptTokens != wantPrompt {
			t.Errorf("%s: spend out=%v prompt=%v, want %d and %d", step, got.SpendOut, got.SpendPromptTokens, wantOut, wantPrompt)
		}
		if got.OutRate != rate {
			t.Errorf("%s: out rate = %+v, want the accountant's %+v", step, got.OutRate, rate)
		}
	}

	meter(120, 4500)
	beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 10, OutputTokens: intPtr(500)})
	check("hook beat after the accountant", 120, 4500)
	meter(130, 4600)
	beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 11, OutputTokens: intPtr(500)})
	check("second hook beat", 130, 4600)
}

// A hook-only seat takes the hook's total, and once the accountant meters it
// the accountant's pair replaces it.
func TestAccountantTakesOverFromAHookTotal(t *testing.T) {
	t.Parallel()
	s := NewStore()
	token := gradedSession(t, s, 0)

	got := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 10, OutputTokens: intPtr(500)})
	if got.SpendOut == nil || *got.SpendOut != 500 || got.SpendPromptTokens != nil {
		t.Fatalf("hook-only seat: out=%v prompt=%v, want 500 and nil", got.SpendOut, got.SpendPromptTokens)
	}
	out, prompt := 40, 900
	s.UpdateSessionSpend(gradedSessionKey, SessionSpend{Out: &out, PromptTokens: &prompt})
	got = beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 11, OutputTokens: intPtr(600)})
	if got.SpendOut == nil || *got.SpendOut != 40 || got.SpendPromptTokens == nil || *got.SpendPromptTokens != 900 {
		t.Errorf("metered seat after a hook beat: out=%v prompt=%v, want 40 and 900", got.SpendOut, got.SpendPromptTokens)
	}
}
