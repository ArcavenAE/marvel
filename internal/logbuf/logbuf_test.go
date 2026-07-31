package logbuf

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestWrite_SplitsOnNewline(t *testing.T) {
	b := New(10)
	_, _ = b.Write([]byte("a\nb\nc\n"))
	got := b.Tail(10)
	want := []string{"a", "b", "c"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestWrite_RingDrops(t *testing.T) {
	b := New(3)
	for i := 0; i < 5; i++ {
		_, _ = fmt.Fprintf(b, "line%d\n", i)
	}
	got := b.Tail(10)
	want := []string{"line2", "line3", "line4"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestWrite_TrailingNoNewline(t *testing.T) {
	b := New(10)
	_, _ = b.Write([]byte("incomplete"))
	got := b.Tail(10)
	if len(got) != 1 || got[0] != "incomplete" {
		t.Errorf("got %v", got)
	}
}

func TestWrite_StripsCR(t *testing.T) {
	b := New(10)
	_, _ = b.Write([]byte("a\r\nb\r\n"))
	got := b.Tail(10)
	want := []string{"a", "b"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestTail_ZeroOrNegative(t *testing.T) {
	b := New(10)
	_, _ = b.Write([]byte("a\nb\n"))
	if got := b.Tail(0); len(got) != 0 {
		t.Errorf("Tail(0): got %v, want empty", got)
	}
	if got := b.Tail(-1); len(got) != 0 {
		t.Errorf("Tail(-1): got %v, want empty", got)
	}
}

func TestTail_ReturnsCopy(t *testing.T) {
	b := New(10)
	_, _ = b.Write([]byte("a\nb\n"))
	got := b.Tail(10)
	got[0] = "mutated"
	got2 := b.Tail(10)
	if got2[0] != "a" {
		t.Errorf("expected tail copy to be independent, buffer corrupted: %v", got2)
	}
}

func TestConcurrentWrites(t *testing.T) {
	b := New(1000)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, _ = fmt.Fprintf(b, "w%d-j%d\n", i, j)
			}
		}(i)
	}
	wg.Wait()
	if got := b.Len(); got != 1000 {
		t.Errorf("Len after 1000 writes across 10 goroutines: got %d, want 1000", got)
	}
}

func TestWrite_LargeSinglePayload(t *testing.T) {
	// Dedup collapses 100 identical "x" lines into 1 stored line plus
	// an active run counter. Cap is irrelevant here — the win of dedup
	// is precisely that an unbounded run consumes a single ring slot.
	b := New(3)
	big := strings.Repeat("x\n", 100)
	_, _ = b.Write([]byte(big))
	if got := b.Len(); got != 1 {
		t.Errorf("expected dedup to collapse 100 identical lines to 1 stored line, got %d", got)
	}
	tail := b.Tail(10)
	if len(tail) != 2 || tail[0] != "x" || tail[1] != "last message repeated 99 times so far" {
		t.Errorf("tail = %v, want [x, last message repeated 99 times so far]", tail)
	}
}

// TestDedup_RunBrokenBySummary: when a different line arrives, the
// prior run's summary is emitted before the new line. Cisco-style
// "last message repeated N times" — N is the count of repeats after
// the first occurrence.
func TestDedup_RunBrokenBySummary(t *testing.T) {
	b := New(100)
	_, _ = b.Write([]byte("A\nA\nA\nB\n"))
	got := b.Tail(10)
	want := []string{"A", "last message repeated 2 times", "B"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestDedup_NonAdjacentNotDeduped: previous-line-only matching means
// A B A B doesn't collapse — each adjacent pair differs. Documented
// design choice (see _kos/ideas/log-rrd-deduplication.md).
func TestDedup_NonAdjacentNotDeduped(t *testing.T) {
	b := New(100)
	_, _ = b.Write([]byte("A\nB\nA\nB\n"))
	got := b.Tail(10)
	want := []string{"A", "B", "A", "B"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestDedup_TailSynthesizesActiveSummary: when an operator queries
// in the middle of an active run, Tail appends a "... so far"
// summary so the run is visible without waiting for a different line.
func TestDedup_TailSynthesizesActiveSummary(t *testing.T) {
	b := New(100)
	_, _ = b.Write([]byte("A\nA\nA\nA\n"))
	got := b.Tail(10)
	want := []string{"A", "last message repeated 3 times so far"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
	// Tail must NOT mutate stored state — the synthesized line is
	// query-time only.
	if b.Len() != 1 {
		t.Errorf("Tail should not store synthesized summary; Len = %d, want 1", b.Len())
	}
}

// TestDedup_FlushEmitsSummary: Flush is the periodic cut for very
// long runs. After Flush, the summary is in the ring and the run is
// cleared so the next identical write starts a fresh run.
func TestDedup_FlushEmitsSummary(t *testing.T) {
	b := New(100)
	_, _ = b.Write([]byte("A\nA\nA\n"))
	b.Flush()
	got := b.Tail(10)
	want := []string{"A", "last message repeated 2 times"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("after Flush: got %v, want %v", got, want)
	}
	// Subsequent identical write starts a fresh run, not continuing
	// the cleared one.
	_, _ = b.Write([]byte("A\n"))
	got = b.Tail(10)
	want = []string{"A", "last message repeated 2 times", "A"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("after Flush + new A: got %v, want %v", got, want)
	}
}

// TestDedup_FlushIdempotent: a second Flush with no intervening
// Write must not append a second summary.
func TestDedup_FlushIdempotent(t *testing.T) {
	b := New(100)
	_, _ = b.Write([]byte("A\nA\n"))
	b.Flush()
	before := b.Len()
	b.Flush()
	if b.Len() != before {
		t.Errorf("Flush not idempotent: %d -> %d", before, b.Len())
	}
}

// TestDedup_SingleOccurrenceNoSummary: a one-shot line followed by
// a different one must NOT emit a "repeated 0 times" summary.
func TestDedup_SingleOccurrenceNoSummary(t *testing.T) {
	b := New(100)
	_, _ = b.Write([]byte("A\nB\n"))
	got := b.Tail(10)
	want := []string{"A", "B"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestDedup_IgnoresLogTimestampPrefix: the daemon logs with the default
// Go log flags (Ldate|Ltime), so every line carries a unique timestamp.
// Before the fingerprint fix, whole-line comparison never matched and the
// dedup never fired. The same message on consecutive lines must collapse
// even though its prefix differs, and the first line's timestamp is kept.
func TestDedup_IgnoresLogTimestampPrefix(t *testing.T) {
	b := New(100)
	_, _ = b.Write([]byte(
		"2026/07/31 10:00:00 ssh: client connected\n" +
			"2026/07/31 10:00:02 ssh: client connected\n" +
			"2026/07/31 10:00:04 ssh: client connected\n",
	))
	got := b.Tail(10)
	want := []string{
		"2026/07/31 10:00:00 ssh: client connected",
		"last message repeated 2 times so far",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if b.Len() != 1 {
		t.Errorf("expected 3 timestamped repeats to collapse to 1 stored line, got %d", b.Len())
	}
}

// TestDedup_MicrosecondPrefix: Lmicroseconds adds a ".000000" field. The
// message body must still fingerprint identically across lines.
func TestDedup_MicrosecondPrefix(t *testing.T) {
	b := New(100)
	_, _ = b.Write([]byte(
		"2026/07/31 10:00:00.123456 tick\n" +
			"2026/07/31 10:00:00.789012 tick\n",
	))
	got := b.Tail(10)
	want := []string{"2026/07/31 10:00:00.123456 tick", "last message repeated 1 times so far"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestDedup_DistinctMessagesWithPrefixNotCollapsed: two different bodies
// under timestamp prefixes must NOT collapse — fingerprint compares the
// body, and the bodies differ.
func TestDedup_DistinctMessagesWithPrefixNotCollapsed(t *testing.T) {
	b := New(100)
	_, _ = b.Write([]byte(
		"2026/07/31 10:00:00 alpha\n" +
			"2026/07/31 10:00:01 beta\n",
	))
	got := b.Tail(10)
	want := []string{"2026/07/31 10:00:00 alpha", "2026/07/31 10:00:01 beta"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestFingerprint(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"ldate_ltime", "2026/07/31 10:00:00 hello world", "hello world"},
		{"microseconds", "2026/07/31 10:00:00.123456 hello", "hello"},
		{"no_prefix", "plain message", "plain message"},
		{"empty_body", "2026/07/31 10:00:00 ", ""},
		{"date_shape_no_space_sep", "2026/07/31 10:00:00x", "2026/07/31 10:00:00x"},
		{"too_short", "2026/07/31", "2026/07/31"},
		{"wrong_separators", "2026-07-31 10:00:00 hello", "2026-07-31 10:00:00 hello"},
		{"letters_in_date", "20AB/07/31 10:00:00 hello", "20AB/07/31 10:00:00 hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fingerprint(tt.in); got != tt.want {
				t.Errorf("fingerprint(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
