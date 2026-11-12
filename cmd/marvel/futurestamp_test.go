package main

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// pinClock holds lastActiveClock at lastActiveNow and clockTolerance at tol
// for one test. These are package variables, so the tests that use it do not
// run in parallel.
func pinClock(t *testing.T, tol time.Duration) {
	t.Helper()
	prevClock, prevTol := lastActiveClock, clockTolerance
	lastActiveClock = func() time.Time { return lastActiveNow }
	clockTolerance = tol
	t.Cleanup(func() { lastActiveClock, clockTolerance = prevClock, prevTol })
}

// A stamp later than the client's now plus the round trip reads ? in every
// cell that shows an age or a reading from it, and a missing stamp reads -.
func TestFutureStampReadsQuestion(t *testing.T) {
	pinClock(t, 2*time.Second)

	future := activeSession("f", -1, api.ContextSourceHeartbeat)
	future.ContextAt = lastActiveNow.Add(time.Hour)
	future.ContextPercent = 40
	future.ContextLimit = 200000
	future.CostTotalUSD = f64p(1.5)
	future.TotalsChangedAt = lastActiveNow.Add(time.Hour)

	row := newSessionRow(future, countServiceHomes([]api.Session{future}))
	for name, got := range map[string]string{
		"LAST-ACTIVE": row.lastActive,
		"LAST-TURN":   row.lastTurn,
		"CTX%":        row.context,
	} {
		if got != "?" {
			t.Errorf("%s with a stamp an hour ahead = %q, want ?", name, got)
		}
	}

	// Inside the round trip it is ordinary clock noise and reads as an age.
	near := activeSession("n", -1, api.ContextSourceHeartbeat)
	near.ContextAt = lastActiveNow.Add(time.Second)
	near.ContextPercent = 40
	near.ContextLimit = 200000
	nrow := newSessionRow(near, countServiceHomes([]api.Session{near}))
	if nrow.lastActive != "0s*" {
		t.Errorf("LAST-ACTIVE with a stamp inside the tolerance = %q, want 0s*", nrow.lastActive)
	}
	if nrow.context != "40%" {
		t.Errorf("CTX%% with a stamp inside the tolerance = %q, want 40%%", nrow.context)
	}

	// A missing stamp is still a dash, not a question mark.
	missing := activeSession("m", -1, api.ContextSourceHeartbeat)
	mrow := newSessionRow(missing, countServiceHomes([]api.Session{missing}))
	if mrow.lastActive != "-" || mrow.context != "-" {
		t.Errorf("missing stamp: LAST-ACTIVE = %q, CTX%% = %q, want - and -", mrow.lastActive, mrow.context)
	}
}

// The tolerance is inclusive, the skew is stamp minus now, and a zero stamp is
// never in the future.
func TestFutureSkewBoundary(t *testing.T) {
	tol := 300 * time.Millisecond
	if _, ok := futureSkew(lastActiveNow.Add(tol), lastActiveNow, tol); ok {
		t.Errorf("stamp at now+tol reads as future, want inside the tolerance")
	}
	skew, ok := futureSkew(lastActiveNow.Add(tol+time.Nanosecond), lastActiveNow, tol)
	if !ok {
		t.Fatalf("stamp 1ns past now+tol is not future")
	}
	if want := tol + time.Nanosecond; skew != want {
		t.Errorf("skew = %v, want stamp minus now = %v", skew, want)
	}
	if _, ok := futureSkew(time.Time{}, lastActiveNow, tol); ok {
		t.Errorf("a zero stamp reads as future, want never")
	}
	if _, ok := futureSkew(lastActiveNow.Add(-time.Hour), lastActiveNow, tol); ok {
		t.Errorf("a past stamp reads as future")
	}
}

// The round trip is the time the list call took on the client's clock, and a
// failed call still reports its error.
func TestRoundTripMeasuredAroundTheCall(t *testing.T) {
	cur := lastActiveNow
	prev := lastActiveClock
	lastActiveClock = func() time.Time { return cur }
	t.Cleanup(func() { lastActiveClock = prev })

	rtt, err := measureRoundTrip(func() error { cur = cur.Add(40 * time.Millisecond); return nil })
	if err != nil || rtt != 40*time.Millisecond {
		t.Errorf("measureRoundTrip = %v, %v; want 40ms, nil", rtt, err)
	}
	boom := errors.New("boom")
	if _, err := measureRoundTrip(func() error { return boom }); !errors.Is(err, boom) {
		t.Errorf("measureRoundTrip error = %v, want %v", err, boom)
	}
}

type skewNote struct {
	Field string `json:"field"`
	State string `json:"state"`
	Skew  string `json:"skew"`
}

// -o json and describe session carry the reason: each session with a future
// stamp gains a clock_skew list naming the field, the state future and the
// skew. Sessions with none gain nothing, and no stamp is rewritten.
func TestClockSkewReasonInJSON(t *testing.T) {
	ahead := activeSession("ahead", -1, api.ContextSourceHeartbeat)
	ahead.ContextAt = lastActiveNow.Add(time.Hour)
	ok := activeSession("ok", time.Minute, api.ContextSourceHeartbeat)

	raw, err := json.Marshal([]api.Session{ahead, ok})
	if err != nil {
		t.Fatal(err)
	}
	out, err := withClockSkew(raw, lastActiveNow, time.Second)
	if err != nil {
		t.Fatalf("withClockSkew: %v", err)
	}
	var list []struct {
		Name      string      `json:"name"`
		ContextAt time.Time   `json:"ContextAt"`
		ClockSkew *[]skewNote `json:"clock_skew"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		t.Fatalf("output is not a session list: %v\n%s", err, out)
	}
	if len(list) != 2 {
		t.Fatalf("got %d sessions, want 2", len(list))
	}
	if list[0].ClockSkew == nil || len(*list[0].ClockSkew) != 1 {
		t.Fatalf("ahead session clock_skew = %v, want one note", list[0].ClockSkew)
	}
	if n := (*list[0].ClockSkew)[0]; n != (skewNote{Field: "ContextAt", State: "future", Skew: "1h0m0s"}) {
		t.Errorf("note = %+v, want ContextAt, future, 1h0m0s", n)
	}
	if !list[0].ContextAt.Equal(ahead.ContextAt) {
		t.Errorf("context_at was rewritten to %v, want %v", list[0].ContextAt, ahead.ContextAt)
	}
	if list[1].ClockSkew != nil {
		t.Errorf("a session with no future stamp gained clock_skew %v", *list[1].ClockSkew)
	}

	// describe session prints one object, not a list.
	one, _ := json.Marshal(ahead)
	oout, err := withClockSkew(one, lastActiveNow, time.Second)
	if err != nil {
		t.Fatalf("withClockSkew on an object: %v", err)
	}
	var obj struct {
		ClockSkew []skewNote `json:"clock_skew"`
	}
	if err := json.Unmarshal(oout, &obj); err != nil || len(obj.ClockSkew) != 1 {
		t.Errorf("object output = %s (err %v), want one clock_skew note", oout, err)
	}
}
