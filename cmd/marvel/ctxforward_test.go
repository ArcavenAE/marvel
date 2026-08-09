package main

import (
	"os"
	"strings"
	"testing"
)

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
	line, _, _, send := renderForward(raw)
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
			line, pct, model, send := renderForward([]byte(tt.payload))
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
