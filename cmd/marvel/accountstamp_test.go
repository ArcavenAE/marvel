package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

func stampWin(pct float64, reset int64) []api.AccountWindow {
	return []api.AccountWindow{{Name: "five_hour", UsedPercent: &pct, ResetsAt: time.Unix(reset, 0).UTC()}}
}

func TestObservationStampKeepsFirstSeenWhileTheFigureIsUnchanged(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	first := observationStamp(dir, "ws", "a", stampWin(40, 1786000000), t0)
	if !first.Equal(t0) {
		t.Fatalf("first sight = %v, want %v", first, t0)
	}
	for _, d := range []time.Duration{time.Second, time.Minute, time.Hour} {
		if got := observationStamp(dir, "ws", "a", stampWin(40, 1786000000), t0.Add(d)); !got.Equal(t0) {
			t.Fatalf("re-render after %v restamped to %v", d, got)
		}
	}
	// A changed percentage and a changed reset are each news.
	t1 := t0.Add(2 * time.Hour)
	if got := observationStamp(dir, "ws", "a", stampWin(55, 1786000000), t1); !got.Equal(t1) {
		t.Fatalf("changed percentage kept the old stamp: %v", got)
	}
	t2 := t1.Add(time.Hour)
	if got := observationStamp(dir, "ws", "a", stampWin(55, 1786100000), t2); !got.Equal(t2) {
		t.Fatalf("changed reset kept the old stamp: %v", got)
	}
}

func TestObservationStampIsPerSeat(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	observationStamp(dir, "ws", "a", stampWin(40, 1786000000), t0)
	if got := observationStamp(dir, "ws", "b", stampWin(40, 1786000000), t0.Add(time.Hour)); !got.Equal(t0.Add(time.Hour)) {
		t.Fatalf("seat b borrowed seat a's stamp: %v", got)
	}
}

// Failing to remember must not become "everything is new".
func TestObservationStampFailsClosed(t *testing.T) {
	t0 := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	if got := observationStamp(filepath.Join(t.TempDir(), "missing", "dir"), "ws", "a", stampWin(40, 1), t0); !got.IsZero() {
		t.Fatalf("unwritable dir stamped %v", got)
	}
	if got := observationStamp("", "ws", "a", stampWin(40, 1), t0); !got.IsZero() {
		t.Fatalf("no dir stamped %v", got)
	}
	if got := observationStamp(t.TempDir(), "ws", "a", nil, t0); !got.IsZero() {
		t.Fatalf("no windows stamped %v", got)
	}
	// A corrupt state file is overwritten with a fresh stamp, not trusted.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".marvel-acct-ws-a.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := observationStamp(dir, "ws", "a", stampWin(40, 1), t0); !got.Equal(t0) {
		t.Fatalf("corrupt state: %v", got)
	}
}
