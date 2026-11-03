// Package goose implements the marvel runtime adapter parse layer for goose
// invoked with `goose run --output-format stream-json`. It translates goose's
// line-delimited JSON stream into the shared marvel event vocabulary,
// mirroring internal/runtime/opencode.
//
// Verified against the recordings in internal/runtime/testdata/goose-arm2
// (goose v1.57.0 on Ollama; see mapping.md). docs/design/goose-adapter-v1.1.md
// rule C4 decides what counts as run: only a tool response says so, never the
// exit code and never the model's text.
package goose

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/runtime/events"
)

// Harness is the value emitted in events.Event.Harness for streams parsed by
// this package.
const Harness = "goose"

// ErrKindVendor tags an error goose itself reported on an `error` line.
const ErrKindVendor = "vendor"

// ErrKindOutputLimit tags the output-token-limit flag on a message's metadata.
// A run cut at its limit can still end with `complete` and exit 0, so the flag
// stays visible (mapping.md).
const ErrKindOutputLimit = "output-limit"

// Config carries the marvel-side context every emitted event needs. Clock
// defaults to time.Now().UTC() when nil; override it for tests.
type Config struct {
	AgentID   string
	Workspace string
	Clock     func() time.Time
}

// Parser translates the goose stream-json stream into normalized events. A
// Parser corresponds to one stream; construct a new one per session.
type Parser struct {
	cfg   Config
	seq   *events.SeqAssigner
	clock func() time.Time

	// open maps the id of a toolRequest with no toolResponse yet to its tool
	// name. It is the same keying C3's idle pause will use.
	open map[string]string
	// denied lists the responses that did not count as run, in stream order.
	denied []events.PermissionDenial
	// limitSeen is set once the output-limit error has been emitted, so the
	// flag is reported once per run.
	limitSeen bool
	// sawJSON is set at the first line that starts with `{`. A line before it
	// that is not JSON is the banner goose prints despite -q and is skipped; one
	// after it is unexpected input and is reported.
	sawJSON bool
}

// NewParser constructs a parser with an internal SeqAssigner.
func NewParser(cfg Config) *Parser {
	return NewParserWithSeq(cfg, events.NewSeqAssigner())
}

// NewParserWithSeq wires an externally-owned SeqAssigner so a single monotonic
// sequence spans multiple event sources on one session.
func NewParserWithSeq(cfg Config, seq *events.SeqAssigner) *Parser {
	clock := cfg.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Parser{cfg: cfg, seq: seq, clock: clock, open: map[string]string{}}
}

// Parse consumes line-delimited JSON from r, one goose line per line, until r
// returns io.EOF or an unrecoverable read error. Blank lines are skipped, and so
// is any line before the first JSON object that does not start with `{`: that is
// the banner goose prints despite -q. After the first object, a line that does
// not start with `{` and a line that does but does not decode both emit
// error{kind:"parse"}, the first with the line bounded. An unknown line or
// content type emits error{kind:"unmapped"} with the raw line preserved. Parsing
// continues in every case. ctx cancellation is polled between lines.
func (p *Parser) Parse(ctx context.Context, r io.Reader, emit func(events.Event)) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		if line[0] != '{' {
			if p.sawJSON {
				emit(p.newEvent(events.KindError, events.ErrorData{
					Kind:    events.ErrKindParse,
					Message: "line is not a json object: " + events.TruncateString(string(line), events.MaxSummaryBytes),
				}, nil))
			}
			continue
		}
		p.sawJSON = true
		p.handleLine(line, emit)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read stream: %w", err)
	}
	return nil
}

type gooseMessage struct {
	Role     string            `json:"role"`
	Content  []json.RawMessage `json:"content"`
	Metadata struct {
		OutputTokenLimitReached bool `json:"outputTokenLimitReached"`
	} `json:"metadata"`
}

type gooseItem struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ID       string `json:"id"`
	ToolCall struct {
		Status string          `json:"status"`
		Value  json.RawMessage `json:"value"`
	} `json:"toolCall"`
	ToolResult struct {
		Status string          `json:"status"`
		Value  json.RawMessage `json:"value"`
	} `json:"toolResult"`
}

type gooseCallValue struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type gooseToolValue struct {
	IsError bool `json:"isError"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

type gooseComplete struct {
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	CacheReadTokens  int `json:"cache_read_input_tokens"`
	CacheWriteTokens int `json:"cache_write_input_tokens"`
}

func (p *Parser) handleLine(line []byte, emit func(events.Event)) {
	raw := make(json.RawMessage, len(line))
	copy(raw, line)

	var head struct {
		Type    string          `json:"type"`
		Message json.RawMessage `json:"message"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		emit(p.newEvent(events.KindError, events.ErrorData{
			Kind:    events.ErrKindParse,
			Message: fmt.Sprintf("malformed json: %v", err),
		}, raw))
		return
	}

	switch head.Type {
	case "message":
		p.handleMessage(head.Message, raw, emit)
	case "complete":
		p.handleComplete(raw, emit)
	case "error":
		p.handleError(head.Error, raw, emit)
	default:
		// "notification" is named in v1 section 3 and has no fixture: it is
		// unmeasured, so it stays visible here (mapping.md).
		emit(p.unmapped(fmt.Sprintf("unknown goose line type: %q", head.Type), raw))
	}
}

func (p *Parser) handleMessage(body, raw json.RawMessage, emit func(events.Event)) {
	var msg gooseMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		emit(p.newEvent(events.KindError, events.ErrorData{
			Kind:    events.ErrKindParse,
			Message: fmt.Sprintf("message: %v", err),
		}, raw))
		return
	}
	role := msg.Role
	if role == "" {
		role = "assistant"
	}

	for _, itemRaw := range msg.Content {
		var item gooseItem
		if err := json.Unmarshal(itemRaw, &item); err != nil {
			emit(p.newEvent(events.KindError, events.ErrorData{
				Kind:    events.ErrKindParse,
				Message: fmt.Sprintf("message content item: %v", err),
			}, raw))
			continue
		}
		switch item.Type {
		case "text":
			emit(p.newEvent(events.KindMessageDelta, events.MessageData{
				Role: role,
				Text: events.TruncateString(item.Text, events.MaxSummaryBytes),
			}, nil))
		case "thinking":
			// No v1 event kind, and one per chunk: an unmapped error each would
			// flood the ring. The choice is recorded in mapping.md.
		case "error":
			// The `error` line that follows a failed request carries the same
			// text, so this item adds no second event (mapping.md).
		case "toolRequest":
			p.handleToolRequest(item, raw, emit)
		case "toolResponse":
			p.handleToolResponse(item, emit)
		default:
			emit(p.unmapped(fmt.Sprintf("unknown goose content item type: %q", item.Type), raw))
		}
	}

	// The flag is read whatever the content holds, so an empty message that
	// carries it still reports it, once per run.
	if msg.Metadata.OutputTokenLimitReached && !p.limitSeen {
		p.limitSeen = true
		emit(p.newEvent(events.KindError, events.ErrorData{
			Kind:    ErrKindOutputLimit,
			Message: "goose reported outputTokenLimitReached",
		}, nil))
	}
}

func (p *Parser) handleToolRequest(item gooseItem, raw json.RawMessage, emit func(events.Event)) {
	var call gooseCallValue
	// A value that is not an object (an error string) leaves call zero, and the
	// request is then reported as unmapped.
	_ = json.Unmarshal(item.ToolCall.Value, &call)
	name := call.Name
	// A response that later names this id still takes its tool name from here,
	// whether or not the request is well formed.
	if item.ID != "" && name != "" {
		p.open[item.ID] = name
	}
	// toolCall.status "success" means the request was well formed, not that
	// the tool ran (C4). Anything else has no call to report.
	if item.ID == "" || name == "" || item.ToolCall.Status != "success" {
		emit(p.unmapped("goose toolRequest without a well-formed call", raw))
		return
	}
	emit(p.newEvent(events.KindToolCall, events.ToolCallData{
		Tool:   name,
		CallID: item.ID,
		Input:  boundRaw(call.Arguments),
	}, nil))
}

// handleToolResponse applies C4: a call ran only when the response status is
// "success" AND value.isError is false. Goose has no denial type of its own, so
// every response that did not run is listed as a denial on session.ended.
func (p *Parser) handleToolResponse(item gooseItem, emit func(events.Event)) {
	var val gooseToolValue
	// A value that is not an object (an error string, say) leaves val zero.
	_ = json.Unmarshal(item.ToolResult.Value, &val)
	ran := item.ToolResult.Status == "success" && !val.IsError

	tool := p.open[item.ID]
	delete(p.open, item.ID)
	if !ran {
		p.denied = append(p.denied, events.PermissionDenial{Tool: tool, CallID: item.ID})
	}

	var out any
	if text := gooseText(val); text != "" {
		out = events.TruncateString(text, events.MaxSummaryBytes)
	} else {
		out = boundRaw(item.ToolResult.Value)
	}
	emit(p.newEvent(events.KindToolResult, events.ToolResultData{
		CallID: item.ID,
		OK:     ran,
		Output: out,
	}, nil))
}

func gooseText(v gooseToolValue) string {
	var parts []string
	for _, c := range v.Content {
		if c.Type == "text" && c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// handleComplete maps goose's terminal line to session.ended. `complete` means
// the run finished, not that every tool ran (C4). Ollama gives no cost and no
// per-request usage, and Bedrock is unrecorded, so none is claimed.
func (p *Parser) handleComplete(raw json.RawMessage, emit func(events.Event)) {
	var c gooseComplete
	if err := json.Unmarshal(raw, &c); err != nil {
		emit(p.newEvent(events.KindError, events.ErrorData{
			Kind:    events.ErrKindParse,
			Message: fmt.Sprintf("complete: %v", err),
		}, raw))
		return
	}
	data := events.SessionEndedData{
		ExitCode: 0,
		Usage:    events.Usage{In: c.InputTokens, Out: c.OutputTokens},
	}
	if c.CacheReadTokens != 0 || c.CacheWriteTokens != 0 || len(p.denied) > 0 {
		data.Metering = &events.Metering{
			CacheReadIn:       c.CacheReadTokens,
			CacheCreationIn:   c.CacheWriteTokens,
			PermissionDenials: append([]events.PermissionDenial(nil), p.denied...),
		}
	}
	emit(p.newEvent(events.KindSessionEnded, data, nil))
}

// handleError maps the `error` line goose writes after a failed request. The
// text is the failure's reason (v1 section 3).
func (p *Parser) handleError(body, raw json.RawMessage, emit func(events.Event)) {
	var text string
	if err := json.Unmarshal(body, &text); err != nil {
		text = string(body)
	}
	emit(p.newEvent(events.KindError, events.ErrorData{
		Kind:    ErrKindVendor,
		Message: events.TruncateString(text, events.MaxSummaryBytes),
	}, raw))
}

// boundedRaw keeps a raw line only when it is within the summary limit. A cut
// line would not be JSON, and a RawMessage that is not JSON fails to marshal, so
// a longer line is dropped; the event's message holds the bounded text.
func boundedRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) > events.MaxSummaryBytes {
		return nil
	}
	return raw
}

func (p *Parser) unmapped(msg string, raw json.RawMessage) events.Event {
	return p.newEvent(events.KindError, events.ErrorData{
		Kind:    events.ErrKindUnmapped,
		Message: msg,
	}, raw)
}

// boundRaw renders a raw JSON value as a bounded summary: a decodable value as
// is when small enough, otherwise truncated bytes.
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

// newEvent stamps an event. Every error event is held to the summary limit: its
// message is cut by TruncateString, and its raw line is kept only within the
// limit (boundedRaw), because a goose line can carry a whole tool output and the
// events.go discipline sends large content by pointer, not inline.
func (p *Parser) newEvent(kind events.Kind, data any, raw json.RawMessage) events.Event {
	if e, ok := data.(events.ErrorData); ok {
		e.Message = events.TruncateString(e.Message, events.MaxSummaryBytes)
		data = e
		raw = boundedRaw(raw)
	}
	return events.Event{
		SchemaVersion: events.SchemaVersion,
		Event:         kind,
		Seq:           p.seq.Next(),
		TS:            p.clock(),
		AgentID:       p.cfg.AgentID,
		Workspace:     p.cfg.Workspace,
		Harness:       Harness,
		Data:          data,
		Raw:           raw,
	}
}
