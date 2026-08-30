package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// TestRateLimitsSurviveParsing is the regression this file exists to
// hold: `rate_limits` used to land on stdin and vanish, because the
// struct did not declare it and encoding/json drops unknown keys without
// complaint. The test asserts the block reaches the struct, not that it
// looks pretty on a line.
//
// PROVENANCE, stated because it is weaker than the fixture beside it.
// statusline-2.1.226-empty.json is a live capture. This one is NOT: it is
// constructed from the shape Claude Code 2.1.226 documents for statusline
// authors ("used_percentage: Percentage of limit used (0-100)",
// "resets_at: Unix epoch seconds when this window resets") and from the
// payload constructor that builds it. No populated payload has been
// observed in traffic here, so this fixture pins marvel's reading of a
// published contract rather than a measurement.
func TestRateLimitsSurviveParsing(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/statusline-2.1.226-rate-limits-synthetic.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var p statuslinePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.RateLimits == nil {
		t.Fatal("rate_limits is nil: the payload's headroom block was dropped again")
	}
	if p.RateLimits.FiveHour == nil || p.RateLimits.SevenDay == nil {
		t.Fatal("want both windows parsed")
	}
	if got := *p.RateLimits.FiveHour.UsedPercentage; got != 41 {
		t.Errorf("five_hour used_percentage = %v, want 41", got)
	}
	if !p.RateLimits.FiveHour.ResetsAt.Valid {
		t.Fatal("five_hour resets_at did not parse")
	}
	if got := p.RateLimits.FiveHour.ResetsAt.Time.Unix(); got != 1786000000 {
		t.Errorf("five_hour resets_at = %d, want 1786000000", got)
	}

	// The whole line, so the account block is provably reachable from the
	// command's output and not merely from the struct.
	now := time.Unix(1785989200, 0).UTC()
	if got := formatRateLimits(p.RateLimits, now); got != "acct 5h 41% (3h0m) 7d 63% (4d18h)" {
		t.Errorf("formatRateLimits = %q", got)
	}
	line, _, _, _, _, send := renderForward(raw)
	if !send {
		t.Errorf("send = false, want true: the context reading is sound")
	}
	if !strings.Contains(line, "CTX 13%") || !strings.Contains(line, "acct 5h 41%") {
		t.Errorf("line = %q, want both the session's CTX and the account's headroom", line)
	}
}

// TestResetInstantShapes covers the trap measured inside one claude
// binary: the statusline emits resets_at as an integer epoch and
// get_usage emits it as an ISO 8601 string. Both are accepted. The last
// case is the load-bearing one: an unfamiliar shape must degrade to an
// absent instant, never to an error, because an error here would fail the
// enclosing payload and take the CTX% feed down with a field it does not
// depend on.
func TestResetInstantShapes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		raw       string
		wantValid bool
		wantUnix  int64
	}{
		{"statusline epoch integer", `1786000000`, true, 1786000000},
		{"get_usage ISO 8601 string", `"2026-08-06T07:06:40Z"`, true, 1786000000},
		{"ISO 8601 with offset", `"2026-08-06T09:06:40+02:00"`, true, 1786000000},
		{"null", `null`, false, 0},
		{"unparseable string", `"whenever"`, false, 0},
		{"unknown object shape", `{"seconds":1786000000}`, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var r resetInstant
			if err := json.Unmarshal([]byte(tt.raw), &r); err != nil {
				t.Fatalf("UnmarshalJSON returned an error, which would fail the whole payload: %v", err)
			}
			if r.Valid != tt.wantValid {
				t.Fatalf("Valid = %v, want %v", r.Valid, tt.wantValid)
			}
			if r.Valid && r.Time.Unix() != tt.wantUnix {
				t.Errorf("Unix = %d, want %d", r.Time.Unix(), tt.wantUnix)
			}
		})
	}
}

// TestRateLimitsDoNotBreakTheContextFeed pins the containment property
// directly: a rate_limits block whose reset instant has an unfamiliar
// shape must leave the context reading intact and forwardable.
func TestRateLimitsDoNotBreakTheContextFeed(t *testing.T) {
	t.Parallel()
	payload := `{"model":{"display_name":"Haiku 4.5"},
		"cost":{"total_cost_usd":0.5},
		"context_window":{"used_percentage":17},
		"rate_limits":{"five_hour":{"used_percentage":41,"resets_at":{"epoch":1786000000}}}}`
	line, pct, model, tokens, window, send := renderForward([]byte(payload))
	if !send {
		t.Fatalf("send = false, want true; line = %q", line)
	}
	if pct != 17 || model != "Haiku 4.5" {
		t.Errorf("pct = %v, model = %q, want 17 and Haiku 4.5", pct, model)
	}
	if window != 0 {
		t.Errorf("window = %d, want 0: this payload declares none", window)
	}
	if tokens != 0 {
		t.Errorf("tokens = %d, want 0: this payload carries no current_usage", tokens)
	}
	if !strings.Contains(line, "acct 5h 41% (resets ?)") {
		t.Errorf("line = %q, want the level kept and the instant marked absent", line)
	}
}

// TestRenderForwardWindow covers the harness-declared window the
// forwarder returns for the heartbeat params. Zero means undeclared, and
// the distinction is the whole point: a consumer must be able to tell "no
// window was declared" from "a window of zero", so an absent
// context_window_size must never surface as a declared 0.
func TestRenderForwardWindow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		payload    string
		wantWindow int
	}{
		{
			name: "declared window is returned",
			payload: `{"model":{"display_name":"Haiku 4.5"},
				"context_window":{"context_window_size":200000,"used_percentage":42}}`,
			wantWindow: 200000,
		},
		{
			name: "undeclared window is zero",
			payload: `{"model":{"display_name":"Haiku 4.5"},
				"context_window":{"used_percentage":42}}`,
			wantWindow: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, _, _, window, send := renderForward([]byte(tt.payload))
			if !send {
				t.Fatal("send = false, want true")
			}
			if window != tt.wantWindow {
				t.Errorf("window = %d, want %d", window, tt.wantWindow)
			}
		})
	}
}

func TestFormatRateLimits(t *testing.T) {
	t.Parallel()
	now := time.Unix(1785989200, 0).UTC() // 3h before the 5h window's reset
	pct := func(f float64) *float64 { return &f }
	at := func(epoch int64) resetInstant {
		return resetInstant{Time: time.Unix(epoch, 0).UTC(), Valid: true}
	}
	tests := []struct {
		name string
		rl   *rateLimits
		want string
	}{
		{"absent block renders nothing", nil, ""},
		{"empty block renders nothing", &rateLimits{}, ""},
		{
			name: "five hour only",
			rl:   &rateLimits{FiveHour: &rateLimitWindow{UsedPercentage: pct(41), ResetsAt: at(1786000000)}},
			want: "acct 5h 41% (3h0m)",
		},
		{
			name: "both windows",
			rl: &rateLimits{
				FiveHour: &rateLimitWindow{UsedPercentage: pct(41), ResetsAt: at(1786000000)},
				SevenDay: &rateLimitWindow{UsedPercentage: pct(63), ResetsAt: at(1786400000)},
			},
			want: "acct 5h 41% (3h0m) 7d 63% (4d18h)",
		},
		{
			// Under an hour the hours term would read 0h, so minutes carry
			// the whole remainder.
			name: "minutes remaining",
			rl:   &rateLimits{FiveHour: &rateLimitWindow{UsedPercentage: pct(94), ResetsAt: at(1785990400)}},
			want: "acct 5h 94% (20m)",
		},
		{
			// The window already rolled over and no fresher payload has
			// arrived, so the level no longer describes anything.
			name: "reset instant already past",
			rl:   &rateLimits{FiveHour: &rateLimitWindow{UsedPercentage: pct(94), ResetsAt: at(1785000000)}},
			want: "acct 5h 94% (stale)",
		},
		{
			// A level with no reset instant is the one thing the triple
			// discipline refuses to present as complete.
			name: "level with no reset instant",
			rl:   &rateLimits{FiveHour: &rateLimitWindow{UsedPercentage: pct(41)}},
			want: "acct 5h 41% (resets ?)",
		},
		{
			// The 100x scaling trap having materialized: a producer sent
			// the raw 0-to-1 fraction's scaling on the wrong channel.
			name: "percentage out of range is refused",
			rl:   &rateLimits{FiveHour: &rateLimitWindow{UsedPercentage: pct(4100), ResetsAt: at(1786000000)}},
			want: "acct 5h ? (raw 4100)",
		},
		{
			name: "negative percentage is refused",
			rl:   &rateLimits{FiveHour: &rateLimitWindow{UsedPercentage: pct(-1), ResetsAt: at(1786000000)}},
			want: "acct 5h ? (raw -1)",
		},
		{
			// A window present but carrying no level is not a reading.
			name: "window with no percentage is skipped",
			rl: &rateLimits{
				FiveHour: &rateLimitWindow{ResetsAt: at(1786000000)},
				SevenDay: &rateLimitWindow{UsedPercentage: pct(63), ResetsAt: at(1786400000)},
			},
			want: "acct 7d 63% (4d18h)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := formatRateLimits(tt.rl, now); got != tt.want {
				t.Errorf("formatRateLimits = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRenderForwardLiveCapture pins the parser to a payload captured from
// a real Claude Code 2.1.226 session on 2026-08-08 rather than to one
// written from the schema. Only the filesystem paths were shortened; the
// key structure is as the harness emitted it.
//
// The session had made no API call, so it exercises the case the feed
// spends its first seconds in: a window is already known, the classes and
// the percentage are both null, and the honest output is absence.
func TestRenderForwardLiveCapture(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/statusline-2.1.226-empty.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	line, _, _, _, _, send := renderForward(raw)
	if send {
		t.Errorf("send = true, want false: a null used_percentage is not a reading")
	}
	if !strings.Contains(line, "Opus 5 (1M context)") {
		t.Errorf("line = %q, want the harness's model display name", line)
	}
	if !strings.Contains(line, "CTX –") {
		t.Errorf("line = %q, want an absent context reading", line)
	}
}

func TestRenderForward(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		payload   string
		wantSend  bool
		wantPct   float64
		wantModel string
		wantIn    string
	}{
		{
			name: "main payload with measurement",
			payload: `{"model":{"display_name":"Haiku 4.5"},
				"cost":{"total_cost_usd":0.0898},
				"context_window":{"used_percentage":17}}`,
			wantSend:  true,
			wantPct:   17,
			wantModel: "Haiku 4.5",
			wantIn:    "CTX 17%",
		},
		{
			// The classes agree with the percentage: 30000 + 4000 + 100000
			// over 800000 is 16.75%, which the harness rounds to 17.
			// Output tokens are excluded from occupancy, so the 2000 here
			// must not move the recomputation.
			name: "classes corroborate the percentage",
			payload: `{"model":{"display_name":"Haiku 4.5"},
				"cost":{"total_cost_usd":0.0898},
				"context_window":{
					"context_window_size":800000,
					"current_usage":{"input_tokens":30000,"output_tokens":2000,
						"cache_creation_input_tokens":4000,"cache_read_input_tokens":100000},
					"used_percentage":17}}`,
			wantSend:  true,
			wantPct:   17,
			wantModel: "Haiku 4.5",
			wantIn:    "CTX 17%",
		},
		{
			// The shape of the failure this guards against: the classes
			// put occupancy at 64% while the payload claims 10%, which is
			// roughly the gap between raw occupancy and the
			// percent-until-auto-compaction figure the harness displays.
			name: "classes contradict the percentage, refuse to forward",
			payload: `{"model":{"display_name":"Haiku 4.5"},
				"cost":{"total_cost_usd":0.0898},
				"context_window":{
					"context_window_size":200000,
					"current_usage":{"input_tokens":28000,"output_tokens":1000,
						"cache_creation_input_tokens":4000,"cache_read_input_tokens":96000},
					"used_percentage":10}}`,
			wantSend: false,
			wantIn:   "harness 10% vs recomputed 64%",
		},
		{
			// Rounding slack is not a contradiction: 16.75% recomputes to
			// 17 and the payload says 16, one point apart.
			name: "rounding difference stays inside tolerance",
			payload: `{"model":{"display_name":"Haiku 4.5"},
				"cost":{"total_cost_usd":0},
				"context_window":{
					"context_window_size":800000,
					"current_usage":{"input_tokens":30000,"output_tokens":0,
						"cache_creation_input_tokens":4000,"cache_read_input_tokens":100000},
					"used_percentage":16}}`,
			wantSend:  true,
			wantPct:   16,
			wantModel: "Haiku 4.5",
			wantIn:    "CTX 16%",
		},
		{
			// Uncorroborated is not suspect. A producer that sends a
			// percentage and no classes keeps the pre-cross-check
			// contract.
			name: "percentage with no classes is forwarded unverified",
			payload: `{"model":{"display_name":"Haiku 4.5"},
				"cost":{"total_cost_usd":0},
				"context_window":{"context_window_size":200000,"used_percentage":42}}`,
			wantSend:  true,
			wantPct:   42,
			wantModel: "Haiku 4.5",
			wantIn:    "CTX 42%",
		},
		{
			// No window means no denominator, so there is nothing to check
			// against and nothing to refuse over.
			name: "classes with no window are forwarded unverified",
			payload: `{"model":{"display_name":"Haiku 4.5"},
				"cost":{"total_cost_usd":0},
				"context_window":{
					"context_window_size":0,
					"current_usage":{"input_tokens":30000,"output_tokens":0,
						"cache_creation_input_tokens":0,"cache_read_input_tokens":0},
					"used_percentage":42}}`,
			wantSend:  true,
			wantPct:   42,
			wantModel: "Haiku 4.5",
			wantIn:    "CTX 42%",
		},
		{
			name: "young session, null percentage",
			payload: `{"model":{"display_name":"Haiku 4.5"},
				"cost":{"total_cost_usd":0},
				"context_window":{"used_percentage":null}}`,
			wantSend: false,
			wantIn:   "CTX –",
		},
		{
			name:     "no context_window at all",
			payload:  `{"model":{"display_name":"Haiku 4.5"}}`,
			wantSend: false,
			wantIn:   "CTX –",
		},
		{
			name: "subagent payload never sends",
			payload: `{"tasks":[
				{"status":"running","tokenCount":11238,"contextWindowSize":200000},
				{"status":"completed","tokenCount":10818,"contextWindowSize":200000}]}`,
			wantSend: false,
			wantIn:   "agents 1/2 running",
		},
		{
			// An over-full window: 150000 against 100000 is 150% raw, and
			// both sides clamp to 100. Without the clamp the recomputation
			// would refuse a reading the harness reported correctly.
			name: "occupancy past the window clamps on both sides",
			payload: `{"model":{"display_name":"Haiku 4.5"},
				"cost":{"total_cost_usd":0},
				"context_window":{
					"context_window_size":100000,
					"current_usage":{"input_tokens":50000,"output_tokens":0,
						"cache_creation_input_tokens":0,"cache_read_input_tokens":100000},
					"used_percentage":100}}`,
			wantSend:  true,
			wantPct:   100,
			wantModel: "Haiku 4.5",
			wantIn:    "CTX 100%",
		},
		{
			name:     "garbage payload",
			payload:  `not json`,
			wantSend: false,
			wantIn:   "unreadable",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			line, pct, model, _, _, send := renderForward([]byte(tt.payload))
			if send != tt.wantSend {
				t.Fatalf("send = %v, want %v", send, tt.wantSend)
			}
			if send && pct != tt.wantPct {
				t.Errorf("pct = %v, want %v", pct, tt.wantPct)
			}
			if send && model != tt.wantModel {
				t.Errorf("model = %q, want %q", model, tt.wantModel)
			}
			if !strings.Contains(line, tt.wantIn) {
				t.Errorf("line = %q, want it to contain %q", line, tt.wantIn)
			}
		})
	}
}
