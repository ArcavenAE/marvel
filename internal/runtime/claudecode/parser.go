package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/arcavenae/marvel/internal/runtime/events"
)

// Harness is the value emitted in events.Event.Harness for streams
// parsed by this package.
const Harness = "claude-code"

// Config carries the marvel-side context every emitted event needs.
// AgentID and Workspace are stamped on every event; HarnessSessionID
// is learned from the vendor system/init line and applied thereafter.
// Clock defaults to time.Now().UTC() when nil — override for tests.
type Config struct {
	AgentID   string
	Workspace string
	Clock     func() time.Time
}

// Parser translates the Claude Code stream-json NDJSON stream into
// normalized events. A Parser instance corresponds to one stream;
// construct a new one per session.
type Parser struct {
	cfg    Config
	seq    *events.SeqAssigner
	sessID string
	clock  func() time.Time
	// model is the init model, which is also the key into the result
	// line's modelUsage map. Per-request message.model omits the [1m]
	// suffix the init model carries, so the two are not interchangeable
	// for a window lookup.
	model string
	// lastRequestID dedupes the per-request usage emission: one API
	// response arrives as one vendor line per content block, each
	// repeating the same message.id and the same usage object.
	lastRequestID string
}

// NewParser constructs a parser with an internal SeqAssigner. Provide
// an explicit assigner via NewParserWithSeq if the caller needs to
// share monotonicity across sources (rare — one parser per session is
// the normal shape).
func NewParser(cfg Config) *Parser {
	return NewParserWithSeq(cfg, events.NewSeqAssigner())
}

// NewParserWithSeq wires an externally-owned SeqAssigner so a single
// monotonic sequence spans multiple event sources on one session.
func NewParserWithSeq(cfg Config, seq *events.SeqAssigner) *Parser {
	clock := cfg.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Parser{
		cfg:   cfg,
		seq:   seq,
		clock: clock,
	}
}

// Parse consumes NDJSON from r, one line per vendor event, until r
// returns io.EOF or an unrecoverable read error. Individual malformed
// lines emit an error{kind:"parse"} event (with the raw line captured)
// and parsing continues — a single bad line does not poison the stream.
// Unknown vendor event types emit error{kind:"unmapped"} with the raw
// vendor JSON preserved, never dropped.
//
// ctx cancellation is polled between lines; a cancelled context ends
// the loop and returns ctx.Err().
func (p *Parser) Parse(ctx context.Context, r io.Reader, emit func(events.Event)) error {
	// Grow the default bufio.Scanner line cap to comfortably hold init
	// events (which include the full tools/agents/skills lists — the
	// hello fixture's init line is ~8.5 KiB, tool-call is ~14 KiB).
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		p.handleLine(line, emit)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read stream: %w", err)
	}
	return nil
}

// handleLine parses one NDJSON line and emits zero or more events.
// A single assistant/user vendor line can contain multiple content
// blocks; each block produces its own event.
func (p *Parser) handleLine(line []byte, emit func(events.Event)) {
	// Copy the raw bytes so the passthrough survives the next scanner
	// tick (bufio.Scanner reuses its internal buffer).
	raw := make(json.RawMessage, len(line))
	copy(raw, line)

	var head struct {
		Type      string `json:"type"`
		Subtype   string `json:"subtype"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		emit(p.newEvent(events.KindError, events.ErrorData{
			Kind:    events.ErrKindParse,
			Message: fmt.Sprintf("malformed json: %v", err),
		}, raw))
		return
	}
	if head.SessionID != "" {
		p.sessID = head.SessionID
	}

	switch head.Type {
	case "system":
		p.handleSystem(head.Subtype, raw, emit)
	case "assistant", "user":
		p.handleMessage(head.Type, raw, emit)
	case "result":
		p.handleResult(raw, emit)
	default:
		emit(p.newEvent(events.KindError, events.ErrorData{
			Kind:    events.ErrKindUnmapped,
			Message: fmt.Sprintf("unknown vendor event type: %q", head.Type),
		}, raw))
	}
}

func (p *Parser) handleSystem(subtype string, raw json.RawMessage, emit func(events.Event)) {
	if subtype != "init" {
		emit(p.newEvent(events.KindError, events.ErrorData{
			Kind:    events.ErrKindUnmapped,
			Message: fmt.Sprintf("unknown system subtype: %q", subtype),
		}, raw))
		return
	}
	var body struct {
		Cwd     string   `json:"cwd"`
		Model   string   `json:"model"`
		Tools   []string `json:"tools"`
		Resumed bool     `json:"resumed"` // absent in fresh sessions; JSON default false
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		emit(p.newEvent(events.KindError, events.ErrorData{
			Kind:    events.ErrKindParse,
			Message: fmt.Sprintf("system/init body: %v", err),
		}, raw))
		return
	}
	p.model = body.Model
	emit(p.newEvent(events.KindSessionStarted, events.SessionStartedData{
		Model:   body.Model,
		Cwd:     body.Cwd,
		Resumed: body.Resumed,
		Tools:   body.Tools,
	}, nil))
}

// contentBlock is the shared shape for assistant/user message content
// blocks. Only fields the parser routes on are declared here; the rest
// stays in raw for follow-on inspection.
type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// messageUsage is the per-request accounting on an assistant line. A
// pointer in the wrapper below because `user` and `tool_result` lines
// carry "usage": null.
type messageUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

func (p *Parser) handleMessage(vendorType string, raw json.RawMessage, emit func(events.Event)) {
	var wrapper struct {
		Message struct {
			ID      string         `json:"id"`
			Model   string         `json:"model"`
			Role    string         `json:"role"`
			Content []contentBlock `json:"content"`
			Usage   *messageUsage  `json:"usage"`
		} `json:"message"`
		// ParentToolUseID is top-level, beside `message`, and is null on
		// every main-agent line in both fixtures. Non-null marks a
		// subagent turn, whose usage belongs to a different context
		// window than the session's own.
		ParentToolUseID string `json:"parent_tool_use_id"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		emit(p.newEvent(events.KindError, events.ErrorData{
			Kind:    events.ErrKindParse,
			Message: fmt.Sprintf("%s body: %v", vendorType, err),
		}, raw))
		return
	}
	role := wrapper.Message.Role
	if role == "" {
		// The `user` and `assistant` wrappers always carry a role in
		// practice; fall back to the outer type so message.completed
		// still names something useful.
		role = vendorType
	}

	p.emitRequestUsage(requestLine{
		id:              wrapper.Message.ID,
		model:           wrapper.Message.Model,
		parentToolUseID: wrapper.ParentToolUseID,
		usage:           wrapper.Message.Usage,
	}, emit)

	// One vendor line may carry several blocks (thinking + tool_use,
	// text + tool_use, etc.). Emit one event per block.
	for _, blk := range wrapper.Message.Content {
		p.emitBlock(role, blk, raw, emit)
	}
}

// requestLine is the per-request accounting lifted off one vendor line.
type requestLine struct {
	id              string
	model           string
	parentToolUseID string
	usage           *messageUsage
}

// emitRequestUsage lifts the assistant line's prompt accounting into a
// turn.completed event, which is marvel's only live context-occupancy
// signal for this harness. Four guards, each load-bearing:
//
//   - usage is a pointer, because user/tool_result lines carry null. A
//     zero emitted for those would land in the occupancy level series
//     and read as a compaction on every tool result.
//   - one event per LINE, not per content block: an assistant line whose
//     only block is `thinking` or `tool_use` still carries usage, and
//     those blocks do not map to message.completed.
//   - dedupe on message.id: one API response is split into one vendor
//     line per block, repeating id and usage. Occupancy is a level so a
//     missed dedupe would only inflate the request count, but the count
//     is reported.
//   - parent_tool_use_id rides the event rather than suppressing it. A
//     subagent's prompt is real spend but a different window, so the
//     consumer must be able to tell the two apart; dropping the line
//     would lose the spend, and folding it in silently would replace the
//     session's occupancy level with the subagent's for the duration of
//     the tool call.
func (p *Parser) emitRequestUsage(line requestLine, emit func(events.Event)) {
	u := line.usage
	if u == nil || line.id == "" || line.id == p.lastRequestID {
		return
	}
	p.lastRequestID = line.id
	emit(p.newEvent(events.KindTurnCompleted, events.TurnData{
		UsageDelta: events.Usage{In: u.InputTokens, Out: u.OutputTokens},
		Request: &events.RequestUsage{
			RequestID:       line.id,
			Model:           line.model,
			Layout:          events.LayoutAdditive,
			In:              u.InputTokens,
			Out:             u.OutputTokens,
			CacheReadIn:     u.CacheReadInputTokens,
			CacheCreationIn: u.CacheCreationInputTokens,
			ParentToolUseID: line.parentToolUseID,
			// Total stays 0: Claude publishes no per-request total, so
			// the TotalMismatch invariant is disabled for this harness
			// and the exact session-end reconciliation is its guard.
		},
	}, nil))
}

func (p *Parser) emitBlock(role string, blk contentBlock, raw json.RawMessage, emit func(events.Event)) {
	switch blk.Type {
	case "text":
		emit(p.newEvent(events.KindMessageCompleted, events.MessageData{
			Role: role,
			Text: events.TruncateString(blk.Text, events.MaxSummaryBytes),
		}, nil))
	case "tool_use":
		var input any
		if len(blk.Input) > 0 {
			// Best-effort decode; a decode failure keeps the raw JSON
			// string so the summary is still useful downstream.
			if err := json.Unmarshal(blk.Input, &input); err != nil {
				input = string(blk.Input)
			}
		}
		emit(p.newEvent(events.KindToolCall, events.ToolCallData{
			Tool:   blk.Name,
			CallID: blk.ID,
			Input:  boundInput(input),
		}, nil))
	case "tool_result":
		emit(p.newEvent(events.KindToolResult, events.ToolResultData{
			CallID: blk.ToolUseID,
			OK:     !blk.IsError,
			Output: boundToolResultContent(blk.Content),
		}, nil))
	case "thinking":
		// `thinking` is a real Claude content-block type that isn't
		// in the v1 event vocabulary. Per §3.2 the correct behavior
		// for unknown-but-present vendor content is
		// error{kind:"unmapped"} with raw set — never drop. Follow-on
		// work may promote a first-class kind (see mapping.md).
		emit(p.newEvent(events.KindError, events.ErrorData{
			Kind:    events.ErrKindUnmapped,
			Message: "assistant thinking block (no v1 event kind)",
		}, raw))
	default:
		emit(p.newEvent(events.KindError, events.ErrorData{
			Kind:    events.ErrKindUnmapped,
			Message: fmt.Sprintf("unknown content block type: %q", blk.Type),
		}, raw))
	}
}

// boundInput truncates a decoded tool-use input to keep the event
// under the 64 KiB summary discipline. Strings are truncated in
// place; other types are round-tripped through JSON and truncated
// as bytes if too large.
func boundInput(in any) any {
	if in == nil {
		return nil
	}
	if s, ok := in.(string); ok {
		return events.TruncateString(s, events.MaxSummaryBytes)
	}
	b, err := json.Marshal(in)
	if err != nil || len(b) <= events.MaxSummaryBytes {
		return in
	}
	return events.TruncateString(string(b), events.MaxSummaryBytes)
}

// boundToolResultContent renders a tool_result.content field (which
// may be a string, a list of {type,text} blocks, or arbitrary JSON)
// as a bounded text summary.
func boundToolResultContent(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	// Common shape: [{"type":"text","text":"..."}]
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil && len(blocks) > 0 {
		out := ""
		for i, b := range blocks {
			if b.Type != "text" {
				continue
			}
			if i > 0 {
				out += "\n"
			}
			out += b.Text
		}
		return events.TruncateString(out, events.MaxSummaryBytes)
	}
	// Fall back to string / raw JSON.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return events.TruncateString(s, events.MaxSummaryBytes)
	}
	return events.TruncateString(string(raw), events.MaxSummaryBytes)
}

// resultBody is the subset of the vendor `result` line marvel consumes.
// Field names on the vendor side are inconsistent (snake_case at the top
// level, camelCase inside modelUsage); the tags below mirror the wire
// exactly rather than normalizing.
type resultBody struct {
	Subtype      string  `json:"subtype"`
	IsError      bool    `json:"is_error"`
	StopReason   string  `json:"stop_reason"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	Usage        struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
	DurationMS    int64 `json:"duration_ms"`
	DurationAPIMS int64 `json:"duration_api_ms"`
	TTFTMS        int64 `json:"ttft_ms"`
	TTFTStreamMS  int64 `json:"ttft_stream_ms"`
	NumTurns      int   `json:"num_turns"`
	ModelUsage    map[string]struct {
		InputTokens              int     `json:"inputTokens"`
		OutputTokens             int     `json:"outputTokens"`
		CacheReadInputTokens     int     `json:"cacheReadInputTokens"`
		CacheCreationInputTokens int     `json:"cacheCreationInputTokens"`
		WebSearchRequests        int     `json:"webSearchRequests"`
		CostUSD                  float64 `json:"costUSD"`
		// ContextWindow is the authoritative denominator for context
		// occupancy. Select the entry by the init model, never by
		// ranging the map: a session that routes across models carries
		// several entries with different windows (200000 and 1000000 in
		// the fixtures) and Go map iteration is randomized.
		ContextWindow   int `json:"contextWindow"`
		MaxOutputTokens int `json:"maxOutputTokens"`
	} `json:"modelUsage"`
	// Entry shape is unverified — every fixture we have carries an empty
	// array. The length is authoritative; tool_name/tool_use_id are
	// best-effort and stay empty when the vendor names them otherwise.
	PermissionDenials []struct {
		ToolName  string `json:"tool_name"`
		ToolUseID string `json:"tool_use_id"`
	} `json:"permission_denials"`
}

// metering lifts the accounting fields off a result line. Returns nil
// when the line carries none of them, so consumers can tell "harness
// did not report" from "harness reported zeros".
func (b *resultBody) metering() *events.Metering {
	m := events.Metering{
		DurationMS:      b.DurationMS,
		APIDurationMS:   b.DurationAPIMS,
		TTFTMS:          b.TTFTMS,
		TTFTStreamMS:    b.TTFTStreamMS,
		NumTurns:        b.NumTurns,
		CacheReadIn:     b.Usage.CacheReadInputTokens,
		CacheCreationIn: b.Usage.CacheCreationInputTokens,
	}
	for model, u := range b.ModelUsage {
		if m.ModelUsage == nil {
			m.ModelUsage = make(map[string]events.ModelUsage, len(b.ModelUsage))
		}
		cost := u.CostUSD
		m.ModelUsage[model] = events.ModelUsage{
			In:                u.InputTokens,
			Out:               u.OutputTokens,
			CacheReadIn:       u.CacheReadInputTokens,
			CacheCreationIn:   u.CacheCreationInputTokens,
			WebSearchRequests: u.WebSearchRequests,
			Cost:              &cost,
			ContextWindow:     u.ContextWindow,
			MaxOutputTokens:   u.MaxOutputTokens,
		}
	}
	for _, d := range b.PermissionDenials {
		m.PermissionDenials = append(m.PermissionDenials, events.PermissionDenial{
			Tool:   d.ToolName,
			CallID: d.ToolUseID,
		})
	}
	empty := m.DurationMS == 0 && m.APIDurationMS == 0 && m.TTFTMS == 0 &&
		m.TTFTStreamMS == 0 && m.NumTurns == 0 && m.CacheReadIn == 0 &&
		m.CacheCreationIn == 0 && len(m.ModelUsage) == 0 && len(m.PermissionDenials) == 0
	if empty {
		return nil
	}
	return &m
}

func (p *Parser) handleResult(raw json.RawMessage, emit func(events.Event)) {
	var body resultBody
	if err := json.Unmarshal(raw, &body); err != nil {
		emit(p.newEvent(events.KindError, events.ErrorData{
			Kind:    events.ErrKindParse,
			Message: fmt.Sprintf("result body: %v", err),
		}, raw))
		return
	}
	exit := 0
	if body.IsError {
		exit = 1
	}
	// Reason precedence: explicit stop_reason wins; subtype is the
	// coarse "success"/"error_max_turns"/… fallback.
	reason := body.StopReason
	if reason == "" {
		reason = body.Subtype
	}
	cost := body.TotalCostUSD
	data := events.SessionEndedData{
		Reason:   reason,
		ExitCode: exit,
		Usage: events.Usage{
			In:   body.Usage.InputTokens,
			Out:  body.Usage.OutputTokens,
			Cost: &cost,
		},
		Metering: body.metering(),
	}
	emit(p.newEvent(events.KindSessionEnded, data, nil))
}

func (p *Parser) newEvent(kind events.Kind, data any, raw json.RawMessage) events.Event {
	return events.Event{
		SchemaVersion:    events.SchemaVersion,
		Event:            kind,
		Seq:              p.seq.Next(),
		TS:               p.clock(),
		AgentID:          p.cfg.AgentID,
		Workspace:        p.cfg.Workspace,
		Harness:          Harness,
		HarnessSessionID: p.sessID,
		Data:             data,
		Raw:              raw,
	}
}
