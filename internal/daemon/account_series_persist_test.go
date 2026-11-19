package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

func seriesDaemon(t *testing.T, lockPath, seriesDir, name string) (d *Daemon, sock string) {
	t.Helper()
	dir := t.TempDir()
	d, err := NewWithOptions(Options{
		RootLock:         lockPath,
		PidFile:          filepath.Join(dir, name+".pid"),
		StateBolt:        filepath.Join(dir, name+".bolt"),
		AccountSeriesDir: seriesDir,
	})
	if err != nil {
		t.Fatalf("new daemon %s: %v", name, err)
	}
	return d, testSocket(t, name)
}

// A daemon started over the same series directory reads back the readings the
// earlier one took, so a restart does not empty the quota series.
func TestDaemonKeepsAccountSeriesAcrossARestart(t *testing.T) {
	skipIfNoTmux(t)
	_, lockPath := rootLockPath(t)
	seriesDir := filepath.Join(t.TempDir(), "account-series")
	key := api.AccountKey{Harness: "claude", Backend: api.BackendDefaultName}
	now := time.Now().UTC().Truncate(time.Second)
	pct := 12.0

	first, sock1 := seriesDaemon(t, lockPath, seriesDir, "first")
	if err := first.Start(sock1); err != nil {
		t.Fatalf("start first daemon: %v", err)
	}
	first.accountSeries.Append(key, []api.AccountWindow{{Name: "five_hour", UsedPercent: &pct, ResetsAt: now.Add(time.Hour)}}, "ws/seat", now.Add(-time.Hour))
	first.Stop()

	second, sock2 := seriesDaemon(t, lockPath, seriesDir, "second")
	if err := second.Start(sock2); err != nil {
		t.Fatalf("start second daemon: %v", err)
	}
	t.Cleanup(second.Stop)
	got := second.accountSeries.Samples(key, "five_hour", time.Time{}, time.Time{})
	if len(got) != 1 || got[0].UsedPercent != 12 {
		t.Fatalf("series after the restart = %+v, want the one reading the first daemon took", got)
	}
}

// A second daemon that loses the root lock touches nothing past it, the series
// directory included: it neither prunes nor reads it.
func TestDaemonThatLosesTheRootLockLeavesTheSeriesDirAlone(t *testing.T) {
	skipIfNoTmux(t)
	_, lockPath := rootLockPath(t)
	seriesDir := filepath.Join(t.TempDir(), "account-series")
	if err := os.MkdirAll(seriesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(seriesDir, "2020-01-01.jsonl")
	if err := os.WriteFile(stale, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	first, sock1 := seriesDaemon(t, lockPath, "", "first")
	if err := first.Start(sock1); err != nil {
		t.Fatalf("start first daemon: %v", err)
	}
	t.Cleanup(first.Stop)

	second, sock2 := seriesDaemon(t, lockPath, seriesDir, "second")
	if err := second.Start(sock2); err == nil {
		second.Stop()
		t.Fatal("second daemon under the same root started; it must refuse")
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("the refused daemon pruned the series directory: %v", err)
	}
}
