package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
)

// A row's own tolerance, the round trip of the read that produced it, decides
// whether its stamp is a future one. The same stamp is inside one row's
// tolerance and past another's, so a fan-out across clusters of different
// speeds judges each cluster by its own trip.
func TestRowToleranceDecidesTheCell(t *testing.T) {
	pinClock(t, 0)

	slow := activeSession("slow", -1, api.ContextSourceHeartbeat)
	slow.ContextAt = lastActiveNow.Add(time.Hour)
	slow.ContextPercent, slow.ContextLimit = 40, 200000
	slow.ClockTolerance = 2 * time.Hour
	fast := slow
	fast.Name = "fast"
	fast.ClockTolerance = time.Millisecond

	srow := newSessionRow(slow, countServiceHomes([]api.Session{slow}))
	frow := newSessionRow(fast, countServiceHomes([]api.Session{fast}))
	if srow.lastActive == "?" || srow.context != "40%" {
		t.Errorf("row with a 2h tolerance: LAST-ACTIVE = %q, CTX%% = %q, want an age and 40%%", srow.lastActive, srow.context)
	}
	if frow.lastActive != "?" || frow.context != "?" {
		t.Errorf("row with a 1ms tolerance: LAST-ACTIVE = %q, CTX%% = %q, want ? and ?", frow.lastActive, frow.context)
	}
}

// LAST-TURN takes the row's tolerance too: a stamp one second ahead, inside a
// two second trip, reads an age. This one passes before the change as well; it
// is a guard for the comparison in lastTurnCell, not a red test.
func TestLastTurnInsideToleranceReadsAge(t *testing.T) {
	pinClock(t, 2*time.Second)
	s := turnSession(-1)
	s.TotalsChangedAt = lastActiveNow.Add(time.Second)
	if got := lastTurnCell(s, lastActiveNow); got != "0s" {
		t.Errorf("LAST-TURN with a stamp inside the tolerance = %q, want 0s", got)
	}
	ahead := turnSession(-1)
	ahead.TotalsChangedAt = lastActiveNow.Add(3 * time.Second)
	if got := lastTurnCell(ahead, lastActiveNow); got != "?" {
		t.Errorf("LAST-TURN with a stamp past the tolerance = %q, want ?", got)
	}
}

// readCluster times its own call, so each cluster's rows carry that cluster's
// round trip. The clock advances 25ms on every read, so the trip is what one
// pair of reads around the call sees.
func TestReadClusterTimesItsOwnCall(t *testing.T) {
	f := startFake(t, fakeAnswers, []api.Session{row("w", "a")})
	clusterConfig(t, config.Cluster{Name: "a", Socket: f.sock})
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	target, err := resolveCluster(cfg, "a")
	if err != nil {
		t.Fatal(err)
	}

	cur := lastActiveNow
	prev := lastActiveClock
	lastActiveClock = func() time.Time { cur = cur.Add(25 * time.Millisecond); return cur }
	t.Cleanup(func() { lastActiveClock = prev })

	params, _ := json.Marshal(getRequest("sessions", false))
	rec, rows, _ := readCluster(target, params, 5*time.Second)
	if rec.Outcome != outcomeAnswered || len(rows) != 1 {
		t.Fatalf("outcome = %q, rows = %d; want answered and one row", rec.Outcome, len(rows))
	}
	if got := rows[0].ClockTolerance; got < 25*time.Millisecond || got > 100*time.Millisecond {
		t.Errorf("row ClockTolerance = %v, want the call's round trip on the pinned clock (25ms to 100ms)", got)
	}
}

type fanRowDoc struct {
	Cluster   string     `json:"cluster"`
	Name      string     `json:"Name"`
	ClockSkew []skewNote `json:"clock_skew"`
}

// get sessions --cluster a,b -o json gives each row with a future stamp the
// clock_skew reason, as the single-cluster read does, judged by that row's
// own tolerance. A row with no future stamp gains nothing.
func TestFanOutDocumentCarriesClockSkew(t *testing.T) {
	pinClock(t, 0)

	slow := api.Session{Workspace: "w", Name: "slow", Cluster: "a", ClockTolerance: 2 * time.Hour}
	slow.ContextAt = lastActiveNow.Add(time.Hour)
	fast := api.Session{Workspace: "w", Name: "fast", Cluster: "b", ClockTolerance: time.Millisecond}
	fast.ContextAt = lastActiveNow.Add(time.Hour)
	ok := api.Session{Workspace: "w", Name: "ok", Cluster: "b", ClockTolerance: time.Millisecond}
	ok.ContextAt = lastActiveNow.Add(-time.Minute)

	doc := buildFanOutDocument(nil, []api.Session{slow, fast, ok})
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Sessions []fanRowDoc `json:"sessions"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	byName := map[string]fanRowDoc{}
	for _, r := range got.Sessions {
		byName[r.Name] = r
	}
	if n := byName["fast"].ClockSkew; len(n) != 1 || n[0] != (skewNote{Field: "ContextAt", State: "future", Skew: "1h0m0s"}) {
		t.Errorf("row inside no tolerance: clock_skew = %+v, want one ContextAt future 1h0m0s note", n)
	}
	if n := byName["slow"].ClockSkew; len(n) != 0 {
		t.Errorf("row with a 2h tolerance: clock_skew = %+v, want none", n)
	}
	if n := byName["ok"].ClockSkew; len(n) != 0 {
		t.Errorf("row with a past stamp: clock_skew = %+v, want none", n)
	}
}

// End to end through the real command: two fake clusters each serve a session
// stamped an hour ahead, and the -o json document gives both rows the reason.
func TestFanOutGetJSONCarriesClockSkew(t *testing.T) {
	ahead := row("w", "ahead")
	ahead.ContextAt = time.Now().Add(time.Hour)
	a := startFake(t, fakeAnswers, []api.Session{ahead})
	b := startFake(t, fakeAnswers, []api.Session{ahead})
	clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "b", Socket: b.sock})

	_, stdout, _ := runFanGet(t, "get", "sessions", "--cluster", "a,b", "-o", "json")
	var doc struct {
		Sessions []fanRowDoc `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("not a document: %v\n%s", err, stdout)
	}
	if len(doc.Sessions) != 2 {
		t.Fatalf("got %d rows, want 2\n%s", len(doc.Sessions), stdout)
	}
	for _, r := range doc.Sessions {
		if len(r.ClockSkew) != 1 || r.ClockSkew[0].State != "future" || !strings.HasPrefix(r.ClockSkew[0].Skew, "59m") && !strings.HasPrefix(r.ClockSkew[0].Skew, "1h") {
			t.Errorf("cluster %s row clock_skew = %+v, want one future note near 1h", r.Cluster, r.ClockSkew)
		}
	}
}
