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

// Codex writes the account's rate-limit windows into the same token_count
// records the occupancy reader uses: payload.rate_limits, with a primary and
// optionally a secondary window, each carrying used_percent, window_minutes
// and resets_at as unix epoch seconds. The block is null on some records, so
// the newest record that carries one is not always the newest token_count.
// Shape read from the real rollout in testdata/rollout-compaction.jsonl
// (finding-050 section 2a): {"limit_id":"codex","primary":{"resets_at":
// 1785937989,"used_percent":22.0,"window_minutes":10080}}.

// ErrNoRateLimits reports that no record in the tail carried a usable
// rate_limits block. Like ErrNoSample it is not a fault: the caller holds
// whatever it knew.
var ErrNoRateLimits = errors.New("codex rollout: no usable rate_limits record in tail")

// Window is one rate-limit window.
type Window struct {
	// Name is "five_hour" or "seven_day" for the two window lengths the
	// harness family reports, otherwise "<minutes>m", otherwise the slot
	// name ("primary", "secondary") when no length was declared.
	Name        string
	UsedPercent float64
	// ResetsAt is zero when the record gave none.
	ResetsAt time.Time
}

// RateLimits is one observation of the account's windows and when codex wrote
// it (zero when the timestamp did not parse).
type RateLimits struct {
	Windows []Window
	TS      time.Time
}

// ReadRateLimits returns the newest usable rate_limits block in the rollout
// at path.
func ReadRateLimits(path string) (RateLimits, error) {
	f, err := os.Open(path)
	if err != nil {
		return RateLimits{}, fmt.Errorf("open rollout: %w", err)
	}
	defer f.Close() //nolint:errcheck // read-only handle
	info, err := f.Stat()
	if err != nil {
		return RateLimits{}, fmt.Errorf("stat rollout: %w", err)
	}
	return readRateLimitsFrom(f, info.Size())
}

func readRateLimitsFrom(r io.ReaderAt, size int64) (RateLimits, error) {
	for _, window := range tailWindows {
		start := size - window
		if start < 0 {
			start = 0
		}
		buf := make([]byte, size-start)
		if _, err := r.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
			return RateLimits{}, fmt.Errorf("read rollout tail: %w", err)
		}
		if rl, ok := newestRateLimits(buf, start > 0); ok {
			return rl, nil
		}
		if start == 0 {
			break
		}
	}
	return RateLimits{}, ErrNoRateLimits
}

// newestRateLimits scans a tail buffer as newestSample does (dropping a
// fragment at either end) and returns the newest block by the record's own
// timestamp, falling back to file order when a timestamp is missing.
func newestRateLimits(buf []byte, partialHead bool) (RateLimits, bool) {
	if partialHead {
		i := bytes.IndexByte(buf, '\n')
		if i < 0 {
			return RateLimits{}, false
		}
		buf = buf[i+1:]
	}
	i := bytes.LastIndexByte(buf, '\n')
	if i < 0 {
		return RateLimits{}, false
	}
	buf = buf[:i+1]

	var out RateLimits
	var found bool
	for len(buf) > 0 {
		i := bytes.IndexByte(buf, '\n')
		line := buf[:i]
		buf = buf[i+1:]
		if !bytes.Contains(line, []byte("rate_limits")) {
			continue
		}
		rl, ok := rateLimitsFromLine(line)
		if !ok {
			continue
		}
		switch {
		case !found, out.TS.IsZero() || rl.TS.IsZero(), !rl.TS.Before(out.TS):
			out, found = rl, true
		}
	}
	return out, found
}

type rateLimitsLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   *struct {
		Type       string `json:"type"`
		RateLimits *struct {
			Primary   *rateLimitWindow `json:"primary"`
			Secondary *rateLimitWindow `json:"secondary"`
		} `json:"rate_limits"`
	} `json:"payload"`
}

type rateLimitWindow struct {
	UsedPercent   *float64 `json:"used_percent"`
	WindowMinutes int      `json:"window_minutes"`
	ResetsAt      *float64 `json:"resets_at"`
}

func rateLimitsFromLine(line []byte) (RateLimits, bool) {
	var rec rateLimitsLine
	if err := json.Unmarshal(line, &rec); err != nil {
		return RateLimits{}, false
	}
	if rec.Type != "event_msg" || rec.Payload == nil || rec.Payload.Type != "token_count" || rec.Payload.RateLimits == nil {
		return RateLimits{}, false
	}
	var out RateLimits
	for slot, w := range map[string]*rateLimitWindow{"primary": rec.Payload.RateLimits.Primary, "secondary": rec.Payload.RateLimits.Secondary} {
		if w == nil || w.UsedPercent == nil {
			continue
		}
		win := Window{Name: windowName(slot, w.WindowMinutes), UsedPercent: *w.UsedPercent}
		if w.ResetsAt != nil {
			win.ResetsAt = time.Unix(int64(*w.ResetsAt), 0).UTC()
		}
		out.Windows = append(out.Windows, win)
	}
	if len(out.Windows) == 0 {
		return RateLimits{}, false
	}
	sortWindows(out.Windows)
	if ts, err := time.Parse(time.RFC3339, rec.Timestamp); err == nil {
		out.TS = ts.UTC()
	}
	return out, true
}

func windowName(slot string, minutes int) string {
	switch minutes {
	case 0:
		return slot
	case 300:
		return "five_hour"
	case 10080:
		return "seven_day"
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}

func sortWindows(ws []Window) {
	for i := 1; i < len(ws); i++ {
		for j := i; j > 0 && ws[j].Name < ws[j-1].Name; j-- {
			ws[j], ws[j-1] = ws[j-1], ws[j]
		}
	}
}
