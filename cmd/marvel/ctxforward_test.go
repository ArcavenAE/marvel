package main

import (
	"strings"
	"testing"
)

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
