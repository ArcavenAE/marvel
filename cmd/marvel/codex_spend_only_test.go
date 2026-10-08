package main

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/asof"
)

// A seat whose feed carries spend and no occupancy (codex's running total)
// reaches the store only through UpdateSessionSpend, which leaves ContextAt
// alone. api.Quiet reads only ContextAt, so the seat reads quiet while its rate
// cell says it is spending, and the renderers follow Quiet.
//
// This pins today's behavior, which is the defect: a seat that is working shows
// RATE 0/s and LAST-ACTIVE "-". It is a test of a known gap in the design note
// for marvel#709 (section 10.7), not a statement that the output is right. A fix
// that lets spend count as activity will fail this test, and the test should
// then be rewritten to the new contract.
func TestSpendOnlySeatReadsQuietSoRateIsZeroAndLastActiveIsADash(t *testing.T) {
	st := api.NewStore()
	seat := &api.Session{
		Name: "codex-a", Workspace: "ws", Team: "squad", Role: "worker",
		State: api.SessionRunning, PaneID: "%3",
	}
	if err := st.CreateSession(seat); err != nil {
		t.Fatal(err)
	}
	out, prompt := 5000, 20000
	st.UpdateSessionSpend(seat.Key(), api.SessionSpend{
		Out: &out, PromptTokens: &prompt,
		OutRate: asof.Cell[float64]{
			Value:      42,
			ObservedAt: rateNow.Add(-5 * time.Second),
			ValidUntil: rateNow.Add(api.DefaultQuietWindow),
			Source:     "accountant",
		},
	})
	got, err := st.GetSession(seat.Key())
	if err != nil {
		t.Fatal(err)
	}

	// Setup: the spend landed and the rate cell is live and fresh.
	if got.SpendOut == nil || *got.SpendOut != 5000 || got.OutRate.Value != 42 || got.OutRate.State(rateNow) != asof.Fresh {
		t.Fatalf("setup: spend not stored as expected: out=%v rate=%+v", got.SpendOut, got.OutRate)
	}

	// The measured behavior: spend alone left ContextAt at zero, so the seat is quiet.
	if !got.ContextAt.IsZero() {
		t.Errorf("ContextAt = %v after a spend-only update, want zero", got.ContextAt)
	}
	if !api.Quiet(&got, api.DefaultQuietWindow, rateNow) {
		t.Error("a spend-only seat reads not quiet: the defect this test pins has changed")
	}
	if cell := rateCell(got, rateNow); cell != "0/s" {
		t.Errorf("RATE = %q for a seat spending at 42/s, want %q (the pinned defect)", cell, "0/s")
	}
	if cell := lastActiveCell(got, rateNow); cell != "-" {
		t.Errorf("LAST-ACTIVE = %q, want %q (the pinned defect)", cell, "-")
	}

	// Positive control: the same seat with a context stamp, the other channel,
	// shows its rate, so the zero above comes from Quiet and not from the cell.
	withContext := got
	withContext.ContextAt = rateNow
	if cell := rateCell(withContext, rateNow); cell == "0/s" || cell == "-" {
		t.Errorf("control: RATE = %q with a fresh ContextAt, want a nonzero rate", cell)
	}
}
