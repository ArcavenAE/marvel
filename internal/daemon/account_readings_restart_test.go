package daemon

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/admission"
	"github.com/arcavenae/marvel/internal/api"
)

// A reading reported before a restart is still there after it: `get budgets`
// shows the account as stale with its percent and its reset, not as absent,
// and `get account-readings` lists it. The series already comes back from disk;
// this is the read side of that, which the first daemon after a restart used
// to answer with "none" and "unknown resource type".
func TestAccountReadingSurvivesARestartInBudgets(t *testing.T) {
	second, key, resets := restartedWithOneReading(t)

	// get budgets: the account is there, stale, with its percent and reset.
	resp := second.handleGet(mustMarshal(t, map[string]any{"resource_type": "budgets"}))
	if resp.Error != "" {
		t.Fatalf("get budgets: %s", resp.Error)
	}
	var rows []admission.Row
	if err := json.Unmarshal(resp.Result, &rows); err != nil {
		t.Fatalf("decode budgets: %v", err)
	}
	var found *admission.Row
	for i := range rows {
		if rows[i].Account == key.String() && rows[i].AccountWindow == "five_hour" {
			found = &rows[i]
		}
	}
	if found == nil {
		t.Fatalf("get budgets after the restart has no five_hour row for %s: the reading is absent\nrows: %+v", key, rows)
	}
	if found.Reading != string(api.ReadingStale) {
		t.Errorf("reading = %q, want %q", found.Reading, api.ReadingStale)
	}
	if found.Observed != 12 {
		t.Errorf("observed = %d, want 12", found.Observed)
	}
	if found.ResetsAt == nil || !found.ResetsAt.Equal(resets) {
		t.Errorf("resets_at = %v, want %v", found.ResetsAt, resets)
	}
}

// get account-readings --since 1h30m lists the reading taken before the restart.
func TestAccountReadingsVerbListsAReadingTakenBeforeARestart(t *testing.T) {
	second, key, _ := restartedWithOneReading(t)
	resp := second.handleGet(mustMarshal(t, map[string]any{"resource_type": "account-readings", "since_seconds": 5400}))
	if resp.Error != "" {
		t.Fatalf("get account-readings: %s", resp.Error)
	}
	var samples []api.AccountSample
	if err := json.Unmarshal(resp.Result, &samples); err != nil {
		t.Fatalf("decode account-readings: %v", err)
	}
	if len(samples) != 1 || samples[0].Account != key.String() || samples[0].Window != "five_hour" || samples[0].UsedPercent != 12 {
		t.Fatalf("account-readings --since 1h30m = %+v, want the one five_hour reading at 12", samples)
	}
}

// restartedWithOneReading starts a daemon, has it take one five_hour reading an
// hour old at 12 percent, stops it, and returns a second daemon started over the
// same series directory, with the account key and the reset time of the reading.
func restartedWithOneReading(t *testing.T) (second *Daemon, key api.AccountKey, resets time.Time) {
	t.Helper()
	skipIfNoTmux(t)
	_, lockPath := rootLockPath(t)
	seriesDir := filepath.Join(t.TempDir(), "account-series")
	key = api.AccountKey{Harness: "claude", Backend: api.BackendDefaultName}
	now := time.Now().UTC().Truncate(time.Second)
	pct := 12.0
	resets = now.Add(3 * time.Hour)

	first, sock1 := seriesDaemon(t, lockPath, seriesDir, "first")
	if err := first.Start(sock1); err != nil {
		t.Fatalf("start first daemon: %v", err)
	}
	first.accountSeries.Append(key, []api.AccountWindow{{Name: "five_hour", UsedPercent: &pct, ResetsAt: resets}}, "ws/seat", now.Add(-time.Hour))
	first.Stop()

	var sock2 string
	second, sock2 = seriesDaemon(t, lockPath, seriesDir, "second")
	if err := second.Start(sock2); err != nil {
		t.Fatalf("start second daemon: %v", err)
	}
	t.Cleanup(second.Stop)

	return second, key, resets
}
