// Package events defines the marvel runtime adapter event vocabulary —
// the normalized shape adapters emit after digesting a harness-specific
// telemetry stream (Claude Code stream-json, Codex --json, OpenCode SSE,
// etc.). This is the marvel-internal seam; it is DISTINCT from
// internal/events (which is the daemon's control-plane state-transition
// ring). Both use the type name Event; callers importing both should alias.
//
// The vocabulary and frame are specified in
// aae-orc/docs/design/director-envelope-and-adapter-events.md §3.
// The pointer-not-payload discipline (64 KiB soft cap on data summaries)
// is inherited from the co-designed director envelope; large content
// should be summarized here and referenced by pointer in a lifted envelope.
package events

import (
	"encoding/json"
	"sync/atomic"
	"time"
)

// SchemaVersion is the currently emitted event-frame schema version.
// Bumped only for breaking changes to the top-level frame; additive
// changes to per-kind `data` payloads do not bump this.
const SchemaVersion = 1

// Kind identifies an event type. The v1 vocabulary is closed to the
// twelve constants below; unknown vendor events map to KindError with
// data.kind = "unmapped" rather than dropping.
type Kind string

const (
	// KindSessionStarted — harness session came up. data: SessionStartedData.
	KindSessionStarted Kind = "session.started"
	// KindSessionEnded — harness session terminated. data: SessionEndedData.
	KindSessionEnded Kind = "session.ended"
	// KindTurnStarted — a request/response turn began. data: TurnData.
	KindTurnStarted Kind = "turn.started"
	// KindTurnCompleted — a request/response turn concluded. data: TurnData.
	KindTurnCompleted Kind = "turn.completed"
	// KindMessageDelta — streaming text delta from a role. data: MessageData.
	KindMessageDelta Kind = "message.delta"
	// KindMessageCompleted — a full message was emitted. data: MessageData.
	KindMessageCompleted Kind = "message.completed"
	// KindToolCall — the agent invoked a tool. data: ToolCallData.
	KindToolCall Kind = "tool.call"
	// KindToolResult — a tool returned to the agent. data: ToolResultData.
	KindToolResult Kind = "tool.result"
	// KindPermissionRequested — the harness is blocking on human approval.
	// data: PermissionRequestedData. This is the escalation event — marvel
	// must surface it to a supervisor per the lift contract (§3.4).
	KindPermissionRequested Kind = "permission.requested"
	// KindAuthRequired — the harness needs credentials refreshed. Typically
	// generated from stderr excerpts, not the primary stream. data: AuthRequiredData.
	KindAuthRequired Kind = "auth.required"
	// KindHealthHeartbeat — adapter-generated liveness. data: HealthHeartbeatData.
	KindHealthHeartbeat Kind = "health.heartbeat"
	// KindError — vendor error event OR adapter-internal problem (parse
	// failure, unmapped vendor event). data: ErrorData.
	KindError Kind = "error"
)

// Event is the normalized frame every adapter emits. JSON tags match the
// spec in aae-orc/docs/design/director-envelope-and-adapter-events.md §3.1
// verbatim (snake_case). `data` is a typed value per Kind; callers
// serialize with encoding/json in the usual way.
type Event struct {
	SchemaVersion    int             `json:"schema_version"`
	Event            Kind            `json:"event"`
	Seq              uint64          `json:"seq"`
	TS               time.Time       `json:"ts"`
	AgentID          string          `json:"agent_id,omitempty"`
	Workspace        string          `json:"workspace,omitempty"`
	Harness          string          `json:"harness"`
	HarnessSessionID string          `json:"harness_session_id,omitempty"`
	Data             any             `json:"data,omitempty"`
	Raw              json.RawMessage `json:"raw,omitempty"`
	Trace            Trace           `json:"trace,omitzero"`
}

// Trace carries out-of-band correlation. OtelTraceparent follows W3C
// Trace Context (`00-<trace-id>-<span-id>-<flags>`); empty when the
// adapter isn't participating in a trace.
type Trace struct {
	OtelTraceparent string `json:"otel_traceparent,omitempty"`
}

// Usage is the shared token/cost shape carried on turn and session events.
type Usage struct {
	In   int      `json:"in,omitempty"`
	Out  int      `json:"out,omitempty"`
	Cost *float64 `json:"cost,omitempty"`
}

// SessionStartedData — new harness session (or resumed one). Only the
// three spec fields are load-bearing; adapters may attach Tools for
// diagnostic use (bounded by the same summary discipline).
type SessionStartedData struct {
	Model   string   `json:"model,omitempty"`
	Cwd     string   `json:"cwd,omitempty"`
	Resumed bool     `json:"resumed"`
	Tools   []string `json:"tools,omitempty"`
}

// SessionEndedData — harness session terminated. ExitCode 0 = success;
// non-zero maps to Lift() → INFORM(alert) per §3.4.
//
// Metering is an additive v1 field: harnesses that report per-session
// timing and per-model accounting attach it, everything else leaves it
// nil. Adding it does not bump SchemaVersion (per-kind `data` payloads
// grow additively by design).
type SessionEndedData struct {
	Reason   string    `json:"reason,omitempty"`
	ExitCode int       `json:"exit_code"`
	Usage    Usage     `json:"usage,omitzero"`
	Metering *Metering `json:"metering,omitempty"`
}

// Metering carries the accounting a harness reports at session end
// beyond the coarse Usage totals: wall-clock and API timings, turn
// count, cache accounting, per-model breakdown, and the permission
// denials accumulated over the session.
//
// Every field is optional. A harness that reports none of it should
// leave SessionEndedData.Metering nil rather than attaching a zero
// value, so consumers can distinguish "not reported" from "zero".
type Metering struct {
	// DurationMS is wall-clock time for the whole session.
	DurationMS int64 `json:"duration_ms,omitempty"`
	// APIDurationMS is time spent in provider API calls.
	APIDurationMS int64 `json:"duration_api_ms,omitempty"`
	// TTFTMS is time to first token.
	TTFTMS int64 `json:"ttft_ms,omitempty"`
	// TTFTStreamMS is time to first streamed token, which a harness may
	// report separately from TTFTMS when it buffers ahead of the stream.
	TTFTStreamMS int64 `json:"ttft_stream_ms,omitempty"`
	// NumTurns counts request/response turns within the session.
	NumTurns int `json:"num_turns,omitempty"`
	// CacheReadIn and CacheCreationIn are prompt-cache token counts.
	// They sit here rather than on Usage because Usage is the shared
	// turn/session shape and its three fields are spec-fixed.
	CacheReadIn     int `json:"cache_read_in,omitempty"`
	CacheCreationIn int `json:"cache_creation_in,omitempty"`
	// ModelUsage breaks the session down by model id. A session that
	// routes across models (a small model for routing, a large one for
	// the answer) reports one entry each.
	ModelUsage map[string]ModelUsage `json:"model_usage,omitempty"`
	// PermissionDenials lists tool invocations the harness refused.
	// Length is the count; entry detail is best-effort.
	PermissionDenials []PermissionDenial `json:"permission_denials,omitempty"`
}

// ModelUsage is per-model accounting within one session.
type ModelUsage struct {
	In                int      `json:"in,omitempty"`
	Out               int      `json:"out,omitempty"`
	CacheReadIn       int      `json:"cache_read_in,omitempty"`
	CacheCreationIn   int      `json:"cache_creation_in,omitempty"`
	WebSearchRequests int      `json:"web_search_requests,omitempty"`
	Cost              *float64 `json:"cost,omitempty"`
}

// PermissionDenial names one refused tool invocation. Tool is the
// harness's tool name; CallID correlates with the tool.call event when
// the harness supplies one.
type PermissionDenial struct {
	Tool   string `json:"tool,omitempty"`
	CallID string `json:"call_id,omitempty"`
}

// TurnData — usage delta at turn boundaries. May be zero-valued when
// the harness does not attribute usage per turn.
type TurnData struct {
	UsageDelta Usage `json:"usage_delta,omitzero"`
}

// MessageData — content for message.delta and message.completed. Text
// is bounded per Truncate (see 64 KiB discipline).
type MessageData struct {
	Role string `json:"role"`
	Text string `json:"text,omitempty"`
}

// ToolCallData — the agent invoked a tool. CallID correlates with the
// matching tool.result. Input is a summary (map or string); large
// arguments should be truncated by the adapter.
type ToolCallData struct {
	Tool   string `json:"tool"`
	CallID string `json:"call_id,omitempty"`
	Input  any    `json:"input,omitempty"`
}

// ToolResultData — a tool call returned. OK = !is_error in the vendor
// stream. Output is a bounded summary.
type ToolResultData struct {
	CallID string `json:"call_id,omitempty"`
	OK     bool   `json:"ok"`
	Output any    `json:"output,omitempty"`
}

// PermissionRequestedData — blocking approval state. Action names what
// needs approving (e.g. "tool.Bash"); Detail is a short description.
type PermissionRequestedData struct {
	Action string `json:"action"`
	Detail string `json:"detail,omitempty"`
}

// AuthRequiredData — the harness needs re-login/token refresh. Hint is
// a stderr excerpt (kept short — the audit stream carries the full text).
type AuthRequiredData struct {
	Hint string `json:"hint,omitempty"`
}

// HealthHeartbeatData — adapter-generated tick. RSS is bytes when
// known; State is the adapter's view (running, idle, stalled).
type HealthHeartbeatData struct {
	RSS   *int64 `json:"rss,omitempty"`
	State string `json:"state,omitempty"`
}

// ErrorData — either a vendor-reported error or an adapter-internal
// problem. Kind should be one of "unmapped", "parse", "transport", or
// a harness-specific tag.
type ErrorData struct {
	Kind    string `json:"kind"`
	Message string `json:"message,omitempty"`
}

// Well-known ErrorData.Kind values. These are strings (not typed
// constants) so vendor-specific kinds compose freely.
const (
	ErrKindUnmapped  = "unmapped"
	ErrKindParse     = "parse"
	ErrKindTransport = "transport"
)

// SeqAssigner hands out monotonic sequence numbers for a single stream.
// Safe for concurrent use; adapters emitting from multiple goroutines
// should share one assigner per session so consumers can detect gaps.
type SeqAssigner struct {
	n atomic.Uint64
}

// NewSeqAssigner constructs an assigner whose first Next() returns 1.
func NewSeqAssigner() *SeqAssigner {
	return &SeqAssigner{}
}

// Next returns the next sequence number. Sequences start at 1.
func (s *SeqAssigner) Next() uint64 {
	return s.n.Add(1)
}

// Current returns the last-assigned sequence, or 0 if Next has not
// been called. Useful for cheap gap checks in tests.
func (s *SeqAssigner) Current() uint64 {
	return s.n.Load()
}

// MaxSummaryBytes is the soft cap on `data` field summaries — inherited
// from the director envelope 64 KiB rule (control channel discipline).
// Adapters that would exceed this should summarize inline and reference
// full payload by pointer via a subsequent lifted envelope.
const MaxSummaryBytes = 64 * 1024

// TruncateString shortens s to at most n bytes (rune-safe — never
// splits a UTF-8 sequence). When truncated, appends a compact marker
// showing how many bytes were dropped. n <= 0 returns "".
func TruncateString(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	// Reserve room for the marker. If n is too small to hold even a
	// marker + one byte, return the head bytes flat.
	const markerBudget = 24 // "…[+9999999999 more bytes]" is 25; keep it tight
	if n < markerBudget+1 {
		return safeCut(s, n)
	}
	head := safeCut(s, n-markerBudget)
	dropped := len(s) - len(head)
	// Compact marker; the ellipsis makes it unmissable in logs.
	return head + "…[+" + itoa(dropped) + " more bytes]"
}

// safeCut returns the longest prefix of s that is at most n bytes and
// ends on a UTF-8 boundary.
func safeCut(s string, n int) string {
	if n >= len(s) {
		return s
	}
	// Walk back from n while the byte is a UTF-8 continuation (10xxxxxx).
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}

// itoa is a tiny local formatter to avoid pulling strconv into a
// hot-path helper. Non-negative integers only.
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
