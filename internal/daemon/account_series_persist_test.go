package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// A daemon built over the same series directory reads back the readings the
// earlier one took, so a restart does not empty the quota series.
func TestDaemonKeepsAccountSeriesAcrossARestart(t *testing.T) {
	skipIfNoTmux(t)
	dir := filepath.Join(t.TempDir(), "account-series")
	key := api.AccountKey{Harness: "claude", Backend: api.BackendDefaultName}
	now := time.Now().UTC().Truncate(time.Second)
	pct := 12.0

	first, err := NewWithOptions(Options{AccountSeriesDir: dir})
	if err != nil {
		t.Fatalf("first daemon: %v", err)
	}
	first.accountSeries.Append(key, []api.AccountWindow{{Name: "five_hour", UsedPercent: &pct, ResetsAt: now.Add(time.Hour)}}, "ws/seat", now.Add(-time.Hour))

	second, err := NewWithOptions(Options{AccountSeriesDir: dir})
	if err != nil {
		t.Fatalf("second daemon: %v", err)
	}
	got := second.accountSeries.Samples(key, "five_hour", time.Time{}, time.Time{})
	if len(got) != 1 || got[0].UsedPercent != 12 {
		t.Fatalf("series after the restart = %+v, want the one reading the first daemon took", got)
	}
}
