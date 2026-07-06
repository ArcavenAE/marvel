package events

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSeqAssignerMonotonic(t *testing.T) {
	t.Parallel()
	s := NewSeqAssigner()
	if got := s.Current(); got != 0 {
		t.Fatalf("Current before Next = %d, want 0", got)
	}
	for want := uint64(1); want <= 100; want++ {
		if got := s.Next(); got != want {
			t.Fatalf("Next #%d = %d, want %d", want, got, want)
		}
	}
	if got := s.Current(); got != 100 {
		t.Fatalf("Current after 100 Next = %d, want 100", got)
	}
}

func TestSeqAssignerConcurrent(t *testing.T) {
	t.Parallel()
	s := NewSeqAssigner()
	const workers = 8
	const perWorker = 1000
	seen := make(map[uint64]struct{}, workers*perWorker)
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				n := s.Next()
				mu.Lock()
				if _, dup := seen[n]; dup {
					mu.Unlock()
					t.Errorf("duplicate seq %d", n)
					return
				}
				seen[n] = struct{}{}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != workers*perWorker {
		t.Fatalf("unique seqs=%d, want %d", len(seen), workers*perWorker)
	}
	// The final value must equal the total number of assignments; no
	// gaps and no repeats.
	if got := s.Current(); got != uint64(workers*perWorker) {
		t.Fatalf("Current=%d, want %d", got, workers*perWorker)
	}
}

func TestTruncateStringShortStrings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{"empty", "", 100, ""},
		{"n_zero", "abc", 0, ""},
		{"n_negative", "abc", -1, ""},
		{"fits", "hello", 10, "hello"},
		{"exact", "hello", 5, "hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := TruncateString(tt.s, tt.n); got != tt.want {
				t.Errorf("TruncateString(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
			}
		})
	}
}

func TestTruncateStringLong(t *testing.T) {
	t.Parallel()
	s := strings.Repeat("A", 1024)
	got := TruncateString(s, 128)
	if len(got) > 128 {
		t.Fatalf("len=%d, want <=128", len(got))
	}
	if !strings.Contains(got, "more bytes]") {
		t.Errorf("truncation marker missing: %q", got)
	}
	// Head must be original bytes, no substitution.
	if !strings.HasPrefix(got, "AAAA") {
		t.Errorf("head not preserved: %q", got)
	}
}

func TestTruncateStringUTF8Safe(t *testing.T) {
	t.Parallel()
	// Every rune is 3 bytes (Han); cutting at a non-boundary would
	// produce invalid UTF-8. n chosen to land inside a rune.
	s := strings.Repeat("漢", 50) // 150 bytes
	got := TruncateString(s, 40) // 40 bytes; must cut on rune boundary
	if !utf8Valid(got) {
		t.Fatalf("truncation produced invalid UTF-8: % x", got)
	}
}

// utf8Valid is a lightweight local check to keep the test dependency-free.
func utf8Valid(s string) bool {
	i := 0
	for i < len(s) {
		b := s[i]
		switch {
		case b < 0x80:
			i++
		case b < 0xC2:
			return false
		case b < 0xE0:
			if i+1 >= len(s) || s[i+1]&0xC0 != 0x80 {
				return false
			}
			i += 2
		case b < 0xF0:
			if i+2 >= len(s) || s[i+1]&0xC0 != 0x80 || s[i+2]&0xC0 != 0x80 {
				return false
			}
			i += 3
		case b < 0xF5:
			if i+3 >= len(s) || s[i+1]&0xC0 != 0x80 || s[i+2]&0xC0 != 0x80 || s[i+3]&0xC0 != 0x80 {
				return false
			}
			i += 4
		default:
			return false
		}
	}
	return true
}

func TestEventJSONShape(t *testing.T) {
	t.Parallel()
	// Confirm every JSON tag in the frame matches the design doc §3.1
	// verbatim. A drift here breaks the wire compact with director +
	// downstream consumers.
	cost := 0.42
	ev := Event{
		SchemaVersion:    SchemaVersion,
		Event:            KindSessionEnded,
		Seq:              412,
		TS:               time.Date(2026, 7, 5, 20, 12, 31, 0, time.UTC),
		AgentID:          "reviewer-a",
		Workspace:        "aae-orc",
		Harness:          "claude-code",
		HarnessSessionID: "sess-abc",
		Data: SessionEndedData{
			Reason:   "end_turn",
			ExitCode: 0,
			Usage:    Usage{In: 100, Out: 5, Cost: &cost},
		},
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(b)
	// Field names must be snake_case per the spec.
	mustContain := []string{
		`"schema_version":1`,
		`"event":"session.ended"`,
		`"seq":412`,
		`"ts":"2026-07-05T20:12:31Z"`,
		`"agent_id":"reviewer-a"`,
		`"workspace":"aae-orc"`,
		`"harness":"claude-code"`,
		`"harness_session_id":"sess-abc"`,
		`"reason":"end_turn"`,
		`"exit_code":0`,
		`"in":100`,
		`"out":5`,
		`"cost":0.42`,
	}
	for _, needle := range mustContain {
		if !strings.Contains(got, needle) {
			t.Errorf("missing %q in %s", needle, got)
		}
	}
	// `raw` and `trace` should be absent when zero-valued.
	if strings.Contains(got, `"raw"`) {
		t.Errorf("zero-valued raw should be omitted: %s", got)
	}
	if strings.Contains(got, `"trace"`) {
		t.Errorf("zero-valued trace should be omitted: %s", got)
	}
}

func TestEventJSONWithRaw(t *testing.T) {
	t.Parallel()
	// `raw` passthrough for unmapped events — must be verbatim, not
	// re-marshaled with escape.
	rawVendor := json.RawMessage(`{"type":"unknown-vendor-event","fields":[1,2,3]}`)
	ev := Event{
		SchemaVersion: SchemaVersion,
		Event:         KindError,
		Seq:           1,
		TS:            time.Unix(0, 0).UTC(),
		Harness:       "claude-code",
		Data:          ErrorData{Kind: ErrKindUnmapped, Message: "unknown block type"},
		Raw:           rawVendor,
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(b)
	if !strings.Contains(got, `"raw":{"type":"unknown-vendor-event"`) {
		t.Errorf("raw must be embedded as JSON object, not re-escaped: %s", got)
	}
	if !strings.Contains(got, `"kind":"unmapped"`) {
		t.Errorf("ErrorData.Kind missing: %s", got)
	}
}

func TestMaxSummaryBytesConstant(t *testing.T) {
	t.Parallel()
	// 64 KiB per the director envelope co-design §2.4. If this ever
	// changes, coordinate with the director envelope spec.
	if MaxSummaryBytes != 64*1024 {
		t.Errorf("MaxSummaryBytes=%d, want %d", MaxSummaryBytes, 64*1024)
	}
}
