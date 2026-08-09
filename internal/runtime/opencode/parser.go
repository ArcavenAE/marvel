// Package opencode implements the marvel runtime adapter parse layer for
// OpenCode invoked with `opencode run --format json`. It translates
// OpenCode's line-delimited JSON event stream into the shared marvel event
// vocabulary, mirroring the shape of internal/runtime/claudecode.
//
// Verified against opencode 1.18.5 fixtures (see testdata and mapping.md).
// This parser targets the one-shot `run --format json` surface. The
// server-first `serve` + `attach` SSE surface is richer (it carries real
// session lifecycle and permission events) and is out of scope here.
package opencode

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

// Harness is the value emitted in events.Event.Harness for streams parsed
// by this package.
const Harness = "opencode"

// Config carries the marvel-side context every emitted event needs.
// AgentID and Workspace are stamped on every event; HarnessSessionID is
// learned from the sessionID field present on every line. Clock defaults
// to time.Now().UTC() when nil — override for tests.
type Config struct {
	AgentID   string
	Workspace string
	Clock     func() time.Time
}

// Parser translates the OpenCode `run --format json` stream into
// normalized events. A Parser instance corresponds to one stream;
// construct a new one per session.
type Parser struct {
	cfg    Config
	seq    *events.SeqAssigner
	sessID string
	clock  func() time.Time
}

// NewParser constructs a parser with an internal SeqAssigner.
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
	return &Parser{cfg: cfg, seq: seq, clock: clock}
}

// Parse consumes line-delimited JSON from r, one OpenCode event per line,
// until r returns io.EOF or an unrecoverable read error. A malformed line
// emits error{kind:"parse"} and parsing continues; an unknown event type
// emits error{kind:"unmapped"} with the raw line preserved. ctx
// cancellation is polled between lines.
func (p *Parser) Parse(ctx context.Context, r io.Reader, emit func(events.Event)) error {
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

// ocPart is the nested `part` object every non-error OpenCode event
// carries. One struct covers every part type; only the fields the parser
// routes on per type are read, the rest stay in raw.
type ocPart struct {
	Type   string          `json:"type"`
	Text   string          `json:"text"`
	Tool   string          `json:"tool"`
	CallID string          `json:"callID"`
	State  ocToolState     `json:"state"`
	Tokens ocTokens        `json:"tokens"`
	Cost   float64         `json:"cost"`
	Reason string          `json:"reason"`
	Output json.RawMessage `json:"output"`
}

type ocToolState struct {
	Status string          `json:"status"`
	Input  json.RawMessage `json:"input"`
	Output json.RawMessage `json:"output"`
	Error  string          `json:"error"`
}

type ocTokens struct {
	Input     int `json:"input"`
	Output    int `json:"output"`
	Reasoning int `json:"reasoning"`
	Total     int `json:"total"`
	Cache     struct {
		Write int `json:"write"`
		Read  int `json:"read"`
	} `json:"cache"`
}

func (p *Parser) handleLine(line []byte, emit func(events.Event)) {
	raw := make(json.RawMessage, len(line))
	copy(raw, line)

	var head struct {
		Type      string `json:"type"`
		SessionID string `json:"sessionID"`
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
	case "step_start":
		emit(p.newEvent(events.KindTurnStarted, events.TurnData{}, nil))
	case "step_finish":
		p.handleStepFinish(raw, emit)
	case "text":
		p.handleText(raw, emit)
	case "reasoning":
		// Reasoning parts are the thinking analog with no v1 event kind.
		emit(p.newEvent(events.KindError, events.ErrorData{
			Kind:    events.ErrKindUnmapped,
			Message: "opencode reasoning part (no v1 event kind)",
		}, raw))
	case "tool_use":
		p.handleTool(raw, emit)
	case "error":
		p.handleError(raw, emit)
	default:
		emit(p.newEvent(events.KindError, events.ErrorData{
			Kind:    events.ErrKindUnmapped,
			Message: fmt.Sprintf("unknown opencode event type: %q", head.Type),
		}, raw))
	}
}

func (p *Parser) handleText(raw json.RawMessage, emit func(events.Event)) {
	part, ok := p.decodePart(raw, emit, "text")
	if !ok {
		return
	}
	emit(p.newEvent(events.KindMessageCompleted, events.MessageData{
		Role: "assistant",
		Text: events.TruncateString(part.Text, events.MaxSummaryBytes),
	}, nil))
}

func (p *Parser) handleStepFinish(raw json.RawMessage, emit func(events.Event)) {
	part, ok := p.decodePart(raw, emit, "step_finish")
	if !ok {
		return
	}
	cost := part.Cost
	emit(p.newEvent(events.KindTurnCompleted, events.TurnData{
		UsageDelta: events.Usage{
			In:   part.Tokens.Input,
			Out:  part.Tokens.Output,
			Cost: &cost,
		},
		Request: &events.RequestUsage{
			// Additive is measured, not assumed: on 179 caching turns
			// tokens.input ran BELOW tokens.cache.read (as low as 1 against
			// 35584), which input cannot do if it already contained the
			// cached tokens. testdata/caching.jsonl is one such turn, and
			// AdditiveConfirmed reports it.
			//
			// TotalExcludesCache stays unset because the same measurement
			// falsified it: opencode's total covers the cache classes too.
			// Every one of 215 step_finish rows satisfied total == input +
			// output + reasoning + cache.read + cache.write, and the 17
			// that also satisfied the narrower input + output + reasoning
			// were exactly the 17 non-caching rows, where the two readings
			// are the same arithmetic. Declaring the narrower definition
			// only suppressed TotalMismatch on every caching turn.
			//
			// Residual: cache.write was 0 on all 215 rows, so its share of
			// the total is inferred from cache.read rather than measured. A
			// cache-creation turn would settle it, and until one arrives
			// this reading errs toward a check that can raise a false
			// alarm over one that cannot speak.
			Layout:          events.LayoutAdditive,
			In:              part.Tokens.Input,
			Out:             part.Tokens.Output,
			CacheReadIn:     part.Tokens.Cache.Read,
			CacheCreationIn: part.Tokens.Cache.Write,
			ReasoningOut:    part.Tokens.Reasoning,
			Total:           part.Tokens.Total,
			Cost:            &cost,
		},
	}, nil))
}

func (p *Parser) handleTool(raw json.RawMessage, emit func(events.Event)) {
	part, ok := p.decodePart(raw, emit, "tool_use")
	if !ok {
		return
	}
	emit(p.newEvent(events.KindToolCall, events.ToolCallData{
		Tool:   part.Tool,
		CallID: part.CallID,
		Input:  boundRaw(part.State.Input),
	}, nil))

	// A tool part carries its result inline via state. In run mode the
	// terminal state (completed or error) is what arrives; a streaming
	// (running/pending) state yields only the call.
	switch part.State.Status {
	case "completed", "error":
		emit(p.newEvent(events.KindToolResult, events.ToolResultData{
			CallID: part.CallID,
			OK:     part.State.Status == "completed",
			Output: toolOutput(part.State),
		}, nil))
	}
}

// toolOutput prefers the tool's output; on an error state without output
// it falls back to the error text so the summary is still useful.
func toolOutput(state ocToolState) any {
	if len(state.Output) > 0 {
		return boundRaw(state.Output)
	}
	if state.Error != "" {
		return events.TruncateString(state.Error, events.MaxSummaryBytes)
	}
	return nil
}

func (p *Parser) handleError(raw json.RawMessage, emit func(events.Event)) {
	var body struct {
		Error struct {
			Name string `json:"name"`
			Data struct {
				Message string `json:"message"`
			} `json:"data"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &body)
	kind := body.Error.Name
	if kind == "" {
		kind = "vendor"
	}
	emit(p.newEvent(events.KindError, events.ErrorData{
		Kind:    kind,
		Message: body.Error.Data.Message,
	}, raw))
}

// decodePart unmarshals the nested part object, emitting a parse error and
// returning ok=false when the line is malformed for its declared type.
func (p *Parser) decodePart(raw json.RawMessage, emit func(events.Event), typ string) (ocPart, bool) {
	var body struct {
		Part ocPart `json:"part"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		emit(p.newEvent(events.KindError, events.ErrorData{
			Kind:    events.ErrKindParse,
			Message: fmt.Sprintf("%s part: %v", typ, err),
		}, raw))
		return ocPart{}, false
	}
	return body.Part, true
}

// boundRaw renders a raw JSON value as a bounded summary. A decodable
// value is returned as-is when small enough; otherwise it is truncated as
// bytes.
func boundRaw(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	if len(raw) <= events.MaxSummaryBytes {
		var v any
		if err := json.Unmarshal(raw, &v); err == nil {
			return v
		}
	}
	return events.TruncateString(string(raw), events.MaxSummaryBytes)
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
