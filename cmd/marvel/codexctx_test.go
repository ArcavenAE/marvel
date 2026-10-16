package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// hookPayload builds a codex hook payload in the shape codex-cli 0.146.0
// emits, captured live on 2026-08-09 from a SessionStart hook.
func hookPayload(transcriptPath string) string {
	return fmt.Sprintf(`{"session_id":"019fe41c-efc3-72f2-ad5c-45d8d411e7a0",`+
		`"transcript_path":%q,"cwd":"/w","hook_event_name":"SessionStart",`+
		`"model":"gpt-5.6-sol","permission_mode":"bypassPermissions","source":"startup"}`, transcriptPath)
}

func writeRollout(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	return path
}

const (
	sampleRecord = `{"timestamp":"2026-08-08T20:22:20.000Z","type":"event_msg","payload":{"type":"token_count","info":{` +
		`"total_token_usage":{"input_tokens":28110,"cached_input_tokens":24064,"cache_write_input_tokens":0,"output_tokens":76,"reasoning_output_tokens":32,"total_tokens":28186},` +
		`"last_token_usage":{"input_tokens":129200,"cached_input_tokens":11008,"cache_write_input_tokens":0,"output_tokens":76,"reasoning_output_tokens":32,"total_tokens":129276},` +
		`"model_context_window":258400}}}` + "\n"

	sentinelRecord = `{"timestamp":"2026-08-08T21:05:02.000Z","type":"event_msg","payload":{"type":"token_count","info":{` +
		`"total_token_usage":{"input_tokens":12492734,"cached_input_tokens":11917568,"cache_write_input_tokens":0,"output_tokens":27440,"reasoning_output_tokens":14997,"total_tokens":12520174},` +
		`"last_token_usage":{"input_tokens":0,"cached_input_tokens":0,"cache_write_input_tokens":0,"output_tokens":0,"reasoning_output_tokens":0,"total_tokens":13221},` +
		`"model_context_window":258400}}}` + "\n"

	// noTotalRecord is a usable sample whose info carries no
	// total_token_usage at all.
	noTotalRecord = `{"timestamp":"2026-08-08T20:22:20.000Z","type":"event_msg","payload":{"type":"token_count","info":{` +
		`"last_token_usage":{"input_tokens":129200,"cached_input_tokens":11008,"cache_write_input_tokens":0,"output_tokens":76,"reasoning_output_tokens":32,"total_tokens":129276},` +
		`"model_context_window":258400}}}` + "\n"

	noWindowRecord = `{"timestamp":"2026-08-08T20:22:20.000Z","type":"event_msg","payload":{"type":"token_count","info":{` +
		`"last_token_usage":{"input_tokens":129200,"cached_input_tokens":11008,"cache_write_input_tokens":0,"output_tokens":76,"reasoning_output_tokens":32,"total_tokens":129276}}}}` + "\n"
)

// TestCodexReading is the decision table for the whole feed. Every row
// that sets send=false is a row where the alternative is forwarding
// zero, and zero is the reading that silently disables shift rotation.
func TestCodexReading(t *testing.T) {
	t.Parallel()

	// One rollout per shape, built once so the payloads can point at them.
	good := writeRollout(t, sampleRecord)
	sentinelOnly := writeRollout(t, sentinelRecord)
	afterCompaction := writeRollout(t, sampleRecord+sentinelRecord)
	windowless := writeRollout(t, noWindowRecord)
	noTotal := writeRollout(t, noTotalRecord)
	empty := writeRollout(t, "")
	absent := filepath.Join(t.TempDir(), "never-written.jsonl")

	tests := []struct {
		name       string
		payload    string
		wantSend   bool
		wantPct    float64
		wantModel  string
		wantWin    int
		wantTokens int
		wantOut    *int
	}{
		{
			name:       "a live rollout forwards the level over its own window",
			payload:    hookPayload(good),
			wantSend:   true,
			wantPct:    50,
			wantModel:  "gpt-5.6-sol",
			wantWin:    258400,
			wantTokens: 129200,
			wantOut:    intp(76),
		},
		{
			name:       "a sample with no total forwards the level and no output total",
			payload:    hookPayload(noTotal),
			wantSend:   true,
			wantPct:    50,
			wantModel:  "gpt-5.6-sol",
			wantWin:    258400,
			wantTokens: 129200,
			wantOut:    nil,
		},
		{
			name: "the compaction sentinel does not become a zero reading",
			// The sentinel is the newest record; the answer is the
			// sample before it, at 50% rather than 0%.
			payload:    hookPayload(afterCompaction),
			wantSend:   true,
			wantPct:    50,
			wantModel:  "gpt-5.6-sol",
			wantWin:    258400,
			wantTokens: 129200,
			wantOut:    intp(76),
		},
		{
			name:     "a rollout carrying only a sentinel holds",
			payload:  hookPayload(sentinelOnly),
			wantSend: false,
		},
		{
			name:     "a sample declaring no window holds rather than guessing one",
			payload:  hookPayload(windowless),
			wantSend: false,
		},
		{
			name:     "a rollout codex has not written yet holds",
			payload:  hookPayload(empty),
			wantSend: false,
		},
		{
			name:     "an unreachable path holds",
			payload:  hookPayload(absent),
			wantSend: false,
		},
		{
			name: "a null transcript_path holds",
			// NullableString in `required`: present is not non-null.
			payload:  `{"session_id":"x","transcript_path":null,"hook_event_name":"SessionEnd"}`,
			wantSend: false,
		},
		{
			name:     "a payload with no transcript_path at all holds",
			payload:  `{"session_id":"x","hook_event_name":"SessionStart","model":"gpt-5.6-sol"}`,
			wantSend: false,
		},
		{
			name:     "agent_transcript_path alone is not a session pointer",
			payload:  `{"session_id":"x","agent_transcript_path":"/tmp/subagent.jsonl","hook_event_name":"SubagentStop"}`,
			wantSend: false,
		},
		{
			name:     "an unreadable payload holds",
			payload:  `{"session_id":`,
			wantSend: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pct, model, tokens, window, out, send := codexReading([]byte(tt.payload))
			if send != tt.wantSend {
				t.Fatalf("send = %v, want %v (pct %v window %d)", send, tt.wantSend, pct, window)
			}
			if model != tt.wantModel {
				t.Errorf("model = %q, want %q", model, tt.wantModel)
			}
			if !send {
				if pct != 0 || window != 0 || tokens != 0 || out != nil {
					t.Errorf("held reading carried pct %v tokens %d window %d, want all zero", pct, tokens, window)
				}
				return
			}
			if pct < tt.wantPct-0.01 || pct > tt.wantPct+0.01 {
				t.Errorf("pct = %v, want about %v", pct, tt.wantPct)
			}
			if window != tt.wantWin {
				t.Errorf("window = %d, want %d", window, tt.wantWin)
			}
			if tokens != tt.wantTokens {
				t.Errorf("tokens = %d, want %d (the numerator, Reading.Level)", tokens, tt.wantTokens)
			}
			switch {
			case tt.wantOut == nil && out != nil:
				t.Errorf("output total = %d, want nil: a record with no total must not forward one", *out)
			case tt.wantOut != nil && out == nil:
				t.Errorf("output total = nil, want %d", *tt.wantOut)
			case tt.wantOut != nil && *out != *tt.wantOut:
				t.Errorf("output total = %d, want %d", *out, *tt.wantOut)
			}
		})
	}
}

// TestCodexReadingIgnoresTheCumulativeTotal is the refutation from
// finding-017 §4 held as a regression. The fixture's total_token_usage
// says 28110 and its last_token_usage says 129200; a reader that took
// the cumulative field would report 10.9% where the session is at 50%,
// and the error grows with request count so the longest sessions are the
// most wrong.
func TestCodexReadingIgnoresTheCumulativeTotal(t *testing.T) {
	t.Parallel()
	pct, _, _, _, _, send := codexReading([]byte(hookPayload(writeRollout(t, sampleRecord))))
	if !send {
		t.Fatal("send = false, want a forwardable reading")
	}
	if pct < 49.99 || pct > 50.01 {
		t.Errorf("pct = %v, want 50: total_token_usage was read as the level", pct)
	}
}

// TestCodexHeartbeatParamsCarriesTheSessionToken covers this forwarder's
// own payload. The contract itself is pinned at the boundary instead, in
// internal/daemon: TestEveryProducerShapeIsAdmitted drives every real
// producer's bytes through the real handler, and TestABareMapPayloadIsRefused
// is its negative half. This one stays because it is the cheap local
// check, not because it is the guard.
func TestCodexHeartbeatParamsCarriesTheSessionToken(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		token     string
		wantToken bool
	}{
		{
			name:      "token present is forwarded",
			token:     "abc123",
			wantToken: true,
		},
		{
			// A session spawned before tokens existed has no token to
			// present. The daemon admits that beat and says so on the
			// ring, so sending an empty string would be worse than
			// omitting the key: it hashes to a value that matches no
			// record and turns a documented fail-open into a refusal.
			name:      "no token omits the key rather than sending empty",
			token:     "",
			wantToken: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := codexHeartbeatParams("ws", "sess", tt.token, "gpt-5.6-sol", 94.0, 242696, 258400, nil)

			raw, err := json.Marshal(p)
			if err != nil {
				t.Fatalf("marshal params: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("unmarshal params: %v", err)
			}

			v, present := got["session_token"]
			if present != tt.wantToken {
				t.Errorf("session_token present = %v, want %v (params: %s)", present, tt.wantToken, raw)
			}
			if tt.wantToken && v != tt.token {
				t.Errorf("session_token = %v, want %q", v, tt.token)
			}
			if got["session_key"] != "ws/sess" {
				t.Errorf("session_key = %v, want ws/sess", got["session_key"])
			}
			if got["context_window"] != float64(258400) {
				t.Errorf("context_window = %v, want 258400", got["context_window"])
			}
		})
	}
}

// The output total goes on the wire as output_tokens when the record had one,
// and the key is absent when it had none, so the daemon can tell never
// reported from zero (marvel#927).
func TestCodexHeartbeatParamsCarriesTheOutputTotal(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		out  *int
		want any
	}{
		{name: "a total is forwarded", out: intp(76), want: float64(76)},
		{name: "a declared zero is forwarded", out: intp(0), want: float64(0)},
		{name: "no total omits the key", out: nil, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := codexHeartbeatParams("ws", "sess", "tok", "gpt-5.6-sol", 94.0, 242696, 258400, tt.out)
			raw, err := json.Marshal(p)
			if err != nil {
				t.Fatalf("marshal params: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("unmarshal params: %v", err)
			}
			v, present := got["output_tokens"]
			if tt.want == nil {
				if present {
					t.Errorf("output_tokens present = %v, want the key absent (params: %s)", v, raw)
				}
				return
			}
			if v != tt.want {
				t.Errorf("output_tokens = %v, want %v (params: %s)", v, tt.want, raw)
			}
		})
	}
}
