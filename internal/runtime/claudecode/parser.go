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

func (p *Parser) handleMessage(vendorType string, raw json.RawMessage, emit func(events.Event)) {
	var wrapper struct {
		Message struct {
			Role    string         `json:"role"`
			Content []contentBlock `json:"content"`
		} `json:"message"`
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

	// One vendor line may carry several blocks (thinking + tool_use,
	// text + tool_use, etc.). Emit one event per block.
	for _, blk := range wrapper.Message.Content {
		p.emitBlock(role, blk, raw, emit)
	}
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

func (p *Parser) handleResult(raw json.RawMessage, emit func(events.Event)) {
	var body struct {
		Subtype      string  `json:"subtype"`
		IsError      bool    `json:"is_error"`
		StopReason   string  `json:"stop_reason"`
		TotalCostUSD float64 `json:"total_cost_usd"`
		Usage        struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
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
