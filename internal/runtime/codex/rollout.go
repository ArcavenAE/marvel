package codex

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// The rollout file is codex's only source of context occupancy.
//
// This is not a preference between channels. The `codex exec --json`
// stream's turn.completed usage object is a RUNNING TOTAL: it matches
// the rollout's total_token_usage field for field, and on the smallest
// multi-request turn there is it reported 28110 where the prompt was
// 14105. Differencing does not rescue it, because samples arrive per
// turn rather than per request, so a difference yields the sum of that
// turn's requests. No parser, no manifest window and no arithmetic
// recovers a level from that stream (finding-017 §4), which is why the
// rollout is the only source rather than the preferred one.
//
// The rollout is reached through the hook payload's transcript_path,
// never derived. Codex's own path is
// sessions/YYYY/MM/DD/rollout-<local-ISO-with-dashes>-<uuid>.jsonl, so a
// deriver needs the session's local date, its start timestamp to the
// second in dash-substituted form, the daemon's timezone, and the
// CODEX_HOME in force: four places to be wrong for some sessions and
// right for most.
//
// Occupancy is last_token_usage.input_tokens ALONE. Codex's layout is
// subsumptive (input_tokens already contains cached_input_tokens), which
// is what LayoutSubsumptive on the parser declares and what 2081 scored
// records confirm: In alone never exceeds the declared window and peaks
// at 93.8%, while In + cached + cache_write exceeds it on 801 records
// and reaches 186.6%.

// ErrNoSample reports that no usable token_count record was found, after
// the tail grew to its cap. It is not a failure of the file: a live
// rollout mid-turn can carry a multi-megabyte tool-output record between
// the newest sample and EOF.
//
// The caller must HOLD its previous reading on this error rather than
// report zero. Occupancy is monotone within a compaction generation, so
// a stale reading is safe and a zero one is a lie in the direction that
// silently disables shift rotation.
var ErrNoSample = errors.New("codex rollout: no usable token_count record in tail")

// Reading is one occupancy observation lifted from a rollout file:
// the level, the window declared beside it, and when codex wrote it.
type Reading struct {
	// Level is last_token_usage.input_tokens, the prompt the model must
	// re-read on the next request. Never total_token_usage, which is a
	// cumulative sum that reached 2,653,437 against a 258,400 window in
	// one measured session.
	Level int
	// Window is model_context_window from the SAME record as Level.
	//
	// It is ALREADY effective. ~/.codex/models_cache.json carries
	// context_window 272000 and effective_context_window_percent 95 for
	// every catalog model, and the rollout declares 272000 x 0.95 =
	// 258400. Applying the percentage again lands on 245480, runs 5%
	// pessimistic, and fires shifts early.
	Window int
	// TS is the record's own timestamp, zero when codex wrote none or
	// the value did not parse. Advisory: it dates the reading, and
	// nothing here branches on it.
	TS time.Time
}

// Percent is occupancy against the declared window, clamped to 0-100.
// ok is false when the record declared no window, in which case the
// caller reports absence: a percentage against a guessed denominator
// misreports silently, and an admission gate reading unresolved as 0%
// admits everything (internal/usage/doc.go).
func (r Reading) Percent() (pct float64, ok bool) {
	if r.Window <= 0 {
		return 0, false
	}
	pct = float64(r.Level) / float64(r.Window) * 100
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return pct, true
}

// tailWindows is the grow-on-miss ladder, in bytes.
//
// A FIXED window is unsafe and 64KB was the size first proposed. The
// largest single record in the measured corpus is 1,776,484 bytes and
// the largest byte gap between consecutive token_count records is
// 1,792,084, so a fixed window can land entirely inside one tool-output
// record and see zero complete records. At rest the first rung is
// generous: across 207 files carrying samples, the newest usable record
// began at most 9,909 bytes from EOF, so every one fit in 64KB with
// 6.6x headroom. Mid-turn is the case the ladder exists for, and mid-turn
// is exactly when a tailing reader runs.
//
// The cap is 4MB, 2.2x the largest observed gap. Beyond it the reader
// returns ErrNoSample and the caller holds, which is the same answer a
// larger cap would eventually give at more cost.
var tailWindows = []int64{64 << 10, 256 << 10, 1 << 20, 4 << 20}

// ReadOccupancy returns the newest usable token_count record in the
// rollout at path.
func ReadOccupancy(path string) (Reading, error) {
	f, err := os.Open(path)
	if err != nil {
		return Reading{}, fmt.Errorf("open rollout: %w", err)
	}
	defer f.Close() //nolint:errcheck // read-only handle

	info, err := f.Stat()
	if err != nil {
		return Reading{}, fmt.Errorf("stat rollout: %w", err)
	}
	return readOccupancyFrom(f, info.Size())
}

// readOccupancyFrom walks the tail ladder over r. Split out from
// ReadOccupancy so the ladder is testable without a file per rung.
func readOccupancyFrom(r io.ReaderAt, size int64) (Reading, error) {
	for _, window := range tailWindows {
		start := size - window
		if start < 0 {
			start = 0
		}
		buf := make([]byte, size-start)
		if _, err := r.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
			return Reading{}, fmt.Errorf("read rollout tail: %w", err)
		}
		if reading, ok := newestSample(buf, start > 0); ok {
			return reading, nil
		}
		// Growing past the start of the file cannot find more records.
		if start == 0 {
			break
		}
	}
	return Reading{}, ErrNoSample
}

// newestSample scans a tail buffer forward and returns the LAST usable
// token_count record in it. Forward-and-keep-the-last is deliberate:
// scanning backward would have to re-derive line boundaries from the
// wrong end for no gain, since the buffer is already in memory.
//
// partialHead says the buffer begins mid-file, so its first line is
// almost certainly a fragment and is dropped. A trailing fragment is
// dropped whenever the buffer does not end in a newline, which is the
// state of any rollout being written to right now.
func newestSample(buf []byte, partialHead bool) (Reading, bool) {
	if partialHead {
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		} else {
			return Reading{}, false
		}
	}
	if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
		buf = buf[:i+1]
	} else {
		return Reading{}, false
	}

	var out Reading
	var found bool
	for len(buf) > 0 {
		i := bytes.IndexByte(buf, '\n')
		line := buf[:i]
		buf = buf[i+1:]
		// Cheap prefilter before the parse. A tail can hold a 1.7MB
		// tool-output record; unmarshalling it to learn it is not a
		// sample is the one avoidable cost in this loop. A false
		// positive (the literal inside some tool's output) falls
		// through to the structured check below and is rejected there.
		if !bytes.Contains(line, []byte("token_count")) {
			continue
		}
		if reading, ok := sampleFromLine(line); ok {
			out, found = reading, true
		}
	}
	return out, found
}

// rolloutLine is the subset of a rollout record this reader parses.
// Declaring a field is the only way to keep it: encoding/json drops
// unknown keys silently, so anything absent here is refused in flight
// with no error and nothing to notice.
type rolloutLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   *struct {
		Type string          `json:"type"`
		Info *tokenCountInfo `json:"info"`
	} `json:"payload"`
}

// tokenCountInfo is the token_count payload's info object.
//
// TotalTokenUsage is declared and deliberately never read. It is the
// trap this file exists to avoid: it sits beside last_token_usage with
// the same field names, and it is a cumulative sum. Naming it here makes
// its exclusion a decision on the page rather than a field someone
// mistakes for missing.
type tokenCountInfo struct {
	LastTokenUsage     *rolloutUsage `json:"last_token_usage"`
	TotalTokenUsage    *rolloutUsage `json:"total_token_usage"`
	ModelContextWindow int           `json:"model_context_window"`
}

// rolloutUsage is one usage object. CachedInputTokens is parsed and not
// summed: codex's layout is subsumptive, so InputTokens already contains
// it and adding it double-counts.
type rolloutUsage struct {
	InputTokens       int `json:"input_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	// ReasoningOutputTokens is a SUBSET of OutputTokens, not a term
	// beside it: over the 1665 measured records carrying nonzero
	// reasoning, total equals input + output on all 1665 and
	// input + output + reasoning on none. Declared so that a later
	// reader does not add it to a total.
	ReasoningOutputTokens int `json:"reasoning_output_tokens"`
	TotalTokens           int `json:"total_tokens"`
}

// sampleFromLine lifts one rollout line into a Reading, applying the
// discards. ok is false for every line that is not a usable sample.
func sampleFromLine(line []byte) (Reading, bool) {
	var rec rolloutLine
	if err := json.Unmarshal(line, &rec); err != nil {
		// A malformed line is skipped, never fatal. The commonest cause
		// is a fragment this reader failed to trim, and taking the whole
		// read down for it would blank a feed over a torn write.
		return Reading{}, false
	}
	if rec.Type != "event_msg" || rec.Payload == nil || rec.Payload.Type != "token_count" {
		return Reading{}, false
	}
	// info == null is a real shape: measured once, at a session's first
	// token_count.
	if rec.Payload.Info == nil || rec.Payload.Info.LastTokenUsage == nil {
		return Reading{}, false
	}
	u := rec.Payload.Info.LastTokenUsage

	// THE COMPACTION SENTINEL. At every compaction codex writes a
	// token_count whose last_token_usage is all zeros except
	// total_tokens, sitting between the compacted record and the first
	// post-compaction sample. Reading it as a level reports occupancy 0
	// against a valid 258400 window at the moment the session is most
	// stressed, which is the direction that silently disables shift
	// rotation. Measured 16 times across the corpus.
	if u.InputTokens == 0 && u.TotalTokens > 0 {
		return Reading{}, false
	}
	// A record with no input and no total carries no level either. It
	// never occurred in 2098 measured records, so this branch is
	// reasoning rather than measurement: zero occupancy and no
	// measurement are indistinguishable here, and absence is the safe
	// reading of the two.
	if u.InputTokens == 0 {
		return Reading{}, false
	}

	out := Reading{
		Level:  u.InputTokens,
		Window: rec.Payload.Info.ModelContextWindow,
	}
	if ts, err := time.Parse(time.RFC3339, rec.Timestamp); err == nil {
		out.TS = ts.UTC()
	}
	return out, true
}
