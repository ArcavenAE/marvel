package main

import (
	"os"
	"path/filepath"
	"strconv"
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
	if err := os.WriteFile(filepath.Join(dir, stampFileName("ws", "a")), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := observationStamp(dir, "ws", "a", stampWin(40, 1), t0); !got.Equal(t0) {
		t.Fatalf("corrupt state: %v", got)
	}
}

// Two seats whose names differ only where a sanitizer would merge them must not
// share a stamp file (marvel#551 review).
func TestObservationStampCollidingNamesStaySeparate(t *testing.T) {
	pairs := [][2][2]string{
		{{"a-b", "c"}, {"a", "b-c"}},
		{{"ws", "x.y"}, {"ws", "x_y"}},
		{{"a", "b"}, {"a-b", ""}},
	}
	for _, p := range pairs {
		dir := t.TempDir()
		t0 := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
		observationStamp(dir, p[0][0], p[0][1], stampWin(40, 1786000000), t0)
		t1 := t0.Add(time.Hour)
		if got := observationStamp(dir, p[1][0], p[1][1], stampWin(40, 1786000000), t1); !got.Equal(t1) {
			t.Errorf("%v borrowed %v's stamp: %v", p[1], p[0], got)
		}
		// And the first is still its own, unchanged.
		if got := observationStamp(dir, p[0][0], p[0][1], stampWin(40, 1786000000), t1.Add(time.Hour)); !got.Equal(t0) {
			t.Errorf("%v lost its stamp: %v", p[0], got)
		}
	}
}

// A symlink planted at the old guessable temp name must not be followed.
func TestObservationStampDoesNotFollowAPlantedSymlink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(dir, stampFileName("ws", "a"))
	oldTmp := final + "." + strconv.Itoa(os.Getpid()) + ".tmp"
	for _, planted := range []string{oldTmp, final} {
		if err := os.Symlink(victim, planted); err != nil {
			t.Fatal(err)
		}
	}
	t0 := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	got := observationStamp(dir, "ws", "a", stampWin(40, 1786000000), t0)
	if raw, _ := os.ReadFile(victim); string(raw) != "keep" {
		t.Fatalf("the symlink target was written: %q", raw)
	}
	if !got.Equal(t0) {
		t.Fatalf("stamp = %v", got)
	}
	if info, err := os.Lstat(final); err != nil || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 {
		t.Fatalf("final file: %v %v", info, err)
	}
	_ = os.Remove(oldTmp) // the planted one is the test's, not a leftover
	if left, _ := filepath.Glob(filepath.Join(dir, ".marvel-acct-*.tmp")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}
