package main

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

var costNow = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

// costSession is a statusline seat: a running total, the ring behind it, and a
// heartbeat beat ago. It carries no token stream.
func costSession(name string, total *float64, beat time.Duration, samples ...api.CostSample) api.Session {
	s := api.Session{Name: name, Workspace: "ws", Team: "squad", Role: "worker", State: api.SessionRunning, PaneID: "%3"}
	s.CostTotalUSD = total
	s.CostSamples = samples
	s.LastHeartbeat = costNow.Add(-beat)
	return s
}

func at(ago time.Duration, usd float64) api.CostSample {
	return api.CostSample{At: costNow.Add(-ago), USD: usd}
}

// withGap marks the feed as having come back from a silence ago.
func withGap(s api.Session, ago time.Duration) api.Session {
	s.CostGapAt = costNow.Add(-ago)
	return s
}

func usd(v float64) *float64 { return &v }

func costCells(t *testing.T, sessions ...api.Session) map[string][2]string {
	t.Helper()
	old := rateClock
	rateClock = func() time.Time { return costNow }
	t.Cleanup(func() { rateClock = old })
	table := renderSessionTableCols(sessions, columnsOrFatal(t, "name,tout,rate", nil))
	out := map[string][2]string{}
	for _, line := range strings.Split(strings.TrimSpace(table), "\n")[1:] {
		f := strings.Fields(line)
		if len(f) == 3 {
			out[f[0]] = [2]string{f[1], f[2]}
		}
	}
	return out
}

func TestCostCellsForAStatuslineSeat(t *testing.T) {
	cases := []struct {
		name        string
		session     api.Session
		tout, rate  string
		explanation string
	}{
		{
			"rising", costSession("rising", usd(4.12), 15*time.Second,
				at(12*time.Minute, 3.0), at(5*time.Minute, 3.6), at(time.Minute, 4.12)),
			"~$4.12", "~$0.11/m", "the change over the 10 minute window from the sample at its start",
		},
		{
			"flat", costSession("flat", usd(2), 15*time.Second, at(30*time.Minute, 2)),
			"~$2.00", "~$0.00/m", "an unchanged total is a measured zero",
		},
		{
			"reset", costSession("reset", usd(0.9), 15*time.Second, at(3*time.Minute, 0.5), at(time.Minute, 0.9)),
			"~$0.90", "-", "a baseline younger than the window shows a dash, never a negative",
		},
		{
			"afterreset", costSession("afterreset", usd(0.9), 15*time.Second, at(11*time.Minute, 0.5), at(time.Minute, 0.9)),
			"~$0.90", "~$0.04/m", "a rate resumes once a window of baseline exists",
		},
		{
			"neverneg", costSession("neverneg", usd(1), 15*time.Second, at(20*time.Minute, 5)),
			"~$1.00", "~$0.00/m", "a total below its anchor reads zero, not negative",
		},
		{
			"silent", costSession("silent", usd(4.12), 11*time.Minute, at(30*time.Minute, 3)),
			"~$4.12", "?", "a feed silent past the window reads as expired",
		},
		{
			"gap", withGap(costSession("gap", usd(3), 15*time.Second, at(5*time.Minute, 3)), 5*time.Minute),
			"~$3.00", "?", "a silence inside the window is not a rate",
		},
		{
			"aftergap", withGap(costSession("aftergap", usd(3.5), 15*time.Second, at(11*time.Minute, 3), at(time.Minute, 3.5)), 11*time.Minute),
			"~$3.50", "~$0.05/m", "a rate resumes once the gap is a window behind",
		},
		{
			"missing", costSession("missing", nil, 15*time.Second),
			"-", "-", "a feed that never carried cost shows dashes, not zero",
		},
	}
	var sessions []api.Session
	for _, c := range cases {
		sessions = append(sessions, c.session)
	}
	got := costCells(t, sessions...)
	for _, c := range cases {
		cells, ok := got[c.name]
		if !ok {
			t.Errorf("%s: no row rendered", c.name)
			continue
		}
		if cells[0] != c.tout || cells[1] != c.rate {
			t.Errorf("%s: TOUT, RATE = %q, %q, want %q, %q (%s)", c.name, cells[0], cells[1], c.tout, c.rate, c.explanation)
		}
	}
}

// A seat with a token stream keeps its tokens, whatever its statusline says.
func TestCostCellsYieldToATokenStream(t *testing.T) {
	s := costSession("streamed", usd(9), 15*time.Second, at(30*time.Minute, 1))
	n := 12000
	s.SpendOut = &n
	got := costCells(t, s)["streamed"]
	if strings.Contains(got[0], "$") || strings.Contains(got[1], "$") {
		t.Errorf("a metered seat showed dollars: TOUT %q, RATE %q", got[0], got[1])
	}
}
