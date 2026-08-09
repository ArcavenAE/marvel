package codex

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tokenCountLine builds one token_count record in the shape codex
// writes, so the tests below vary one thing at a time against a real
// record rather than against a struct literal.
func tokenCountLine(in, total, window int) string {
	return tokenCountLineAt("2026-08-08T20:22:20.000Z", in, total, window)
}

// tokenCountLineAt is tokenCountLine with the record's own timestamp
// under the caller's control, for the ordering tests.
func tokenCountLineAt(ts string, in, total, window int) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{`+
		`"total_token_usage":{"input_tokens":999999,"cached_input_tokens":0,"cache_write_input_tokens":0,"output_tokens":0,"reasoning_output_tokens":0,"total_tokens":999999},`+
		`"last_token_usage":{"input_tokens":%d,"cached_input_tokens":11008,"cache_write_input_tokens":0,"output_tokens":76,"reasoning_output_tokens":32,"total_tokens":%d},`+
		`"model_context_window":%d}}}`, ts, in, total, window)
}

// TestSampleFromLineDiscards covers the per-record rules. The first two
// rows are the ones that fail silently in the safe-looking direction:
// both report LOW pressure at HIGH pressure, which is the direction that
// disables shift rotation without an error anywhere.
func TestSampleFromLineDiscards(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		line       string
		wantOK     bool
		wantLevel  int
		wantWindow int
	}{
		{
			name:   "compaction sentinel is discarded",
			line:   tokenCountLine(0, 13221, 258400),
			wantOK: false,
		},
		{
			name:   "info null is discarded",
			line:   `{"timestamp":"2026-08-08T20:22:11.000Z","type":"event_msg","payload":{"type":"token_count","info":null}}`,
			wantOK: false,
		},
		{
			name:   "last_token_usage null is discarded",
			line:   `{"timestamp":"2026-08-08T20:22:11.000Z","type":"event_msg","payload":{"type":"token_count","info":{"model_context_window":258400}}}`,
			wantOK: false,
		},
		{
			name:   "all-zero usage is discarded",
			line:   tokenCountLine(0, 0, 258400),
			wantOK: false,
		},
		{
			name:       "a real sample is kept whole",
			line:       tokenCountLine(14105, 14181, 258400),
			wantOK:     true,
			wantLevel:  14105,
			wantWindow: 258400,
		},
		{
			name:       "a sample without a window keeps its level",
			line:       tokenCountLine(14105, 14181, 0),
			wantOK:     true,
			wantLevel:  14105,
			wantWindow: 0,
		},
		{
			name:   "another event_msg type is not a sample",
			line:   `{"timestamp":"2026-08-08T20:22:10.000Z","type":"event_msg","payload":{"type":"task_started","model_context_window":258400}}`,
			wantOK: false,
		},
		{
			name:   "a response_item is not a sample",
			line:   `{"timestamp":"2026-08-08T20:22:10.000Z","type":"response_item","payload":{"type":"message","content":[{"type":"input_text","text":"token_count"}]}}`,
			wantOK: false,
		},
		{
			name:   "a torn line is skipped rather than fatal",
			line:   `{"timestamp":"2026-08-08T20:22:20.000Z","type":"event_msg","payload":{"type":"token_c`,
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := sampleFromLine([]byte(tt.line))
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (reading %+v)", ok, tt.wantOK, got)
			}
			if !ok {
				return
			}
			if got.Level != tt.wantLevel {
				t.Errorf("Level = %d, want %d", got.Level, tt.wantLevel)
			}
			if got.Window != tt.wantWindow {
				t.Errorf("Window = %d, want %d", got.Window, tt.wantWindow)
			}
		})
	}
}

// TestSentinelDoesNotBecomeTheReading is the headline regression, run
// against a fixture whose LAST token_count is the compaction sentinel.
// A newest-wins reader reports 0% here. The truth is 242504 of 258400,
// which is 93.8%: the fullest the session ever was.
func TestSentinelDoesNotBecomeTheReading(t *testing.T) {
	t.Parallel()
	got, err := ReadOccupancy(filepath.Join("testdata", "rollout-compaction.jsonl"))
	if err != nil {
		t.Fatalf("ReadOccupancy: %v", err)
	}
	if got.Level != 242504 {
		t.Errorf("Level = %d, want 242504: the sentinel was read as the level", got.Level)
	}
	if got.Window != 258400 {
		t.Errorf("Window = %d, want 258400", got.Window)
	}
	pct, ok := got.Percent()
	if !ok {
		t.Fatal("Percent not ok, want a resolved window")
	}
	if pct < 93.8 || pct > 93.9 {
		t.Errorf("Percent = %v, want about 93.85", pct)
	}
	if got.TS.IsZero() {
		t.Error("TS is zero, want the record's own timestamp")
	}
}

// TestReadOccupancyGrowsPastAGiantRecord is the other silent failure.
// The newest sample sits behind a tool-output record larger than the
// first rung of the ladder, so a fixed 64KB window sees one fragment and
// no complete record. Measured justification: the largest single record
// in the corpus is 1,776,484 bytes and the largest gap between
// consecutive samples is 1,792,084.
func TestReadOccupancyGrowsPastAGiantRecord(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	b.WriteString(tokenCountLine(14105, 14181, 258400) + "\n")
	// One record comfortably larger than tailWindows[0], smaller than
	// tailWindows[1].
	b.WriteString(`{"timestamp":"2026-08-08T20:23:00.000Z","type":"response_item","payload":{"type":"message","text":"` +
		strings.Repeat("x", 120<<10) + `"}}` + "\n")
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// The first rung really does miss: assert it, so the test proves the
	// ladder did work rather than that the file happened to be small.
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	size := int64(b.Len())
	tail := make([]byte, tailWindows[0])
	if _, err := f.ReadAt(tail, size-tailWindows[0]); err != nil {
		t.Fatalf("read tail: %v", err)
	}
	if _, ok := newestSample(tail, true); ok {
		t.Fatal("the 64KB rung found a sample: the fixture no longer exercises grow-on-miss")
	}

	got, err := ReadOccupancy(path)
	if err != nil {
		t.Fatalf("ReadOccupancy: %v", err)
	}
	if got.Level != 14105 {
		t.Errorf("Level = %d, want 14105", got.Level)
	}
}

// TestReadOccupancyHoldsRatherThanReportsZero: a rollout with no usable
// sample must be an error the caller can hold on, never a zero reading.
func TestReadOccupancyHoldsRatherThanReportsZero(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{
			name: "no token_count at all",
			body: `{"timestamp":"2026-08-08T20:22:10.000Z","type":"event_msg","payload":{"type":"task_started","model_context_window":258400}}` + "\n",
		},
		{
			name: "only a sentinel",
			body: tokenCountLine(0, 13221, 258400) + "\n",
		},
		{
			name: "empty file",
			body: "",
		},
		{
			name: "one line, never terminated",
			body: tokenCountLine(14105, 14181, 258400),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "rollout.jsonl")
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			got, err := ReadOccupancy(path)
			if !errors.Is(err, ErrNoSample) {
				t.Fatalf("err = %v, want ErrNoSample (reading %+v)", err, got)
			}
			if got.Level != 0 || got.Window != 0 {
				t.Errorf("reading = %+v, want the zero Reading alongside the error", got)
			}
		})
	}
}

// TestReadOccupancyIgnoresAPartialTrailingWrite: codex is appending to
// this file while marvel reads it, so the last line is routinely half
// written. The complete record before it is the answer.
func TestReadOccupancyIgnoresAPartialTrailingWrite(t *testing.T) {
	t.Parallel()
	body := tokenCountLine(14105, 14181, 258400) + "\n" +
		tokenCountLine(99999, 100000, 258400)[:200]
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadOccupancy(path)
	if err != nil {
		t.Fatalf("ReadOccupancy: %v", err)
	}
	if got.Level != 14105 {
		t.Errorf("Level = %d, want 14105: the half-written line was read", got.Level)
	}
}

func TestReadOccupancyMissingFile(t *testing.T) {
	t.Parallel()
	_, err := ReadOccupancy(filepath.Join(t.TempDir(), "absent.jsonl"))
	if err == nil || errors.Is(err, ErrNoSample) {
		t.Fatalf("err = %v, want an open failure distinct from ErrNoSample", err)
	}
}

func TestReadingPercent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		reading Reading
		want    float64
		wantOK  bool
	}{
		{
			name:    "the measured peak",
			reading: Reading{Level: 242504, Window: 258400},
			want:    93.85,
			wantOK:  true,
		},
		{
			name:    "no window is absence, not zero",
			reading: Reading{Level: 14105},
			wantOK:  false,
		},
		{
			// The window is already effective (272000 x 0.95). Anything
			// over it is a schema change rather than a real reading, so
			// it clamps instead of rendering above full.
			name:    "over the window clamps",
			reading: Reading{Level: 300000, Window: 258400},
			want:    100,
			wantOK:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := tt.reading.Percent()
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if got < tt.want-0.01 || got > tt.want+0.01 {
				t.Errorf("Percent = %v, want about %v", got, tt.want)
			}
		})
	}
}

// TestNewestSampleWins: several usable records in one tail resolve to
// the last, because occupancy is a level and latest wins.
func TestNewestSampleWins(t *testing.T) {
	t.Parallel()
	buf := []byte(tokenCountLine(1000, 1076, 258400) + "\n" +
		tokenCountLine(2000, 2076, 258400) + "\n" +
		tokenCountLine(3000, 3076, 258400) + "\n")
	got, ok := newestSample(buf, false)
	if !ok {
		t.Fatal("no sample found")
	}
	if got.Level != 3000 {
		t.Errorf("Level = %d, want 3000", got.Level)
	}
}

// TestNewestSampleOrdersByTimestampNotPosition. Claude Code writes
// records physically later than records with a much older timestamp, 26
// times across 2 of 422 sessions, clustered just before a compaction
// (probe-0tnf). Codex has not been seen doing it, over 210 files and
// every record type, but that absence is too thin to lean on and the
// ordering is one comparison. A last-in-file reader returns 40000 here
// where the session is at 200000.
func TestNewestSampleOrdersByTimestampNotPosition(t *testing.T) {
	t.Parallel()
	buf := []byte(
		tokenCountLineAt("2026-08-08T20:22:20.000Z", 100000, 100076, 258400) + "\n" +
			tokenCountLineAt("2026-08-08T21:44:00.000Z", 200000, 200076, 258400) + "\n" +
			tokenCountLineAt("2026-08-07T19:10:00.000Z", 40000, 40076, 258400) + "\n")
	got, ok := newestSample(buf, false)
	if !ok {
		t.Fatal("no sample found")
	}
	if got.Level != 200000 {
		t.Errorf("Level = %d, want 200000: the back-dated trailing record won", got.Level)
	}
}

// TestNewestSampleFallsBackToFileOrderWhenUndatable: a record with no
// usable timestamp cannot be ordered by time, and must neither win nor
// lose silently. File order decides, and the later record takes it.
func TestNewestSampleFallsBackToFileOrderWhenUndatable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		buf  string
		want int
	}{
		{
			name: "undatable record after a dated one",
			buf: tokenCountLineAt("2026-08-08T20:22:20.000Z", 100000, 100076, 258400) + "\n" +
				tokenCountLineAt("not-a-timestamp", 40000, 40076, 258400) + "\n",
			want: 40000,
		},
		{
			name: "dated record after an undatable one",
			buf: tokenCountLineAt("", 100000, 100076, 258400) + "\n" +
				tokenCountLineAt("2026-08-08T20:22:20.000Z", 40000, 40076, 258400) + "\n",
			want: 40000,
		},
		{
			name: "equal timestamps tie to the later record",
			buf: tokenCountLineAt("2026-08-08T20:22:20.000Z", 100000, 100076, 258400) + "\n" +
				tokenCountLineAt("2026-08-08T20:22:20.000Z", 40000, 40076, 258400) + "\n",
			want: 40000,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := newestSample([]byte(tt.buf), false)
			if !ok {
				t.Fatal("no sample found")
			}
			if got.Level != tt.want {
				t.Errorf("Level = %d, want %d", got.Level, tt.want)
			}
		})
	}
}

// TestNewestSampleDropsThePartialHead: a tail that begins mid-file opens
// on a fragment. Parsing it is harmless (it fails), but a fragment that
// happens to parse would be a record with missing fields, so it is
// dropped by position rather than by luck.
func TestNewestSampleDropsThePartialHead(t *testing.T) {
	t.Parallel()
	full := tokenCountLine(1000, 1076, 258400)
	buf := []byte(full[len(full)-40:] + "\n" + tokenCountLine(2000, 2076, 258400) + "\n")
	got, ok := newestSample(buf, true)
	if !ok {
		t.Fatal("no sample found")
	}
	if got.Level != 2000 {
		t.Errorf("Level = %d, want 2000", got.Level)
	}
	if _, ok := newestSample([]byte("no newline here at all"), true); ok {
		t.Error("a headless buffer with no newline yielded a sample")
	}
}
