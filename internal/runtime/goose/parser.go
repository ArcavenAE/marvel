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

// WatchdogType is the type of the marvel-authored line the watchdog wrapper
// writes as the last line of the stream when it ends a run. It is not a goose
// type.
const WatchdogType = "marvel.watchdog"

// watchdogExit is the exit code the wrapper ends a killed run with.
const watchdogExit = 124

type heldWatchdog struct {
	reason string
	raw    json.RawMessage
}

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
	// watchdog holds a marvel.watchdog line until it is known to be the last.
	watchdog *heldWatchdog
	// completed is set when a complete line has ended the session.
	completed bool
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

// maxLine is the longest stream line the parser reads whole. A longer line is
// read to its newline without being kept, reported as a parse error with a
// bounded prefix, and the stream goes on (a bufio.Scanner would stop the parse
// at it and lose every later line).
const maxLine = 4 << 20

// longLinePrefix is how much of an over-long line the parse error quotes.
const longLinePrefix = 256

// Parse consumes line-delimited JSON from r, one goose line per line, until r
// returns io.EOF or an unrecoverable read error. Blank lines are skipped, and so
// is any line before the first JSON object that does not start with `{`: that is
// the banner goose prints despite -q. After the first object, a line that does
// not start with `{` and a line that does but does not decode both emit
// error{kind:"parse"}, the first with the line bounded. A line over maxLine
// emits error{kind:"parse"} with a bounded prefix and no raw line. An unknown
// line or content type emits error{kind:"unmapped"} with the raw line
// preserved. Parsing continues in every case. ctx cancellation is polled between
// lines.
//
// A `marvel.watchdog` line is the one line the wrapper writes when it ends a run
// (docs/design/goose-adapter-v1.1.md C3). It counts only as the stream's last
// line: it is held until the next line or EOF, and any later line makes it an
// unmapped error, so goose output cannot end a session by imitating it.
func (p *Parser) Parse(ctx context.Context, r io.Reader, emit func(events.Event)) error {
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, size, err := readLine(br)
		if len(line) > 0 || size > 0 {
			p.handleInput(line, size, emit)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				p.finishWatchdog(emit)
				return nil
			}
			return fmt.Errorf("read stream: %w", err)
		}
	}
}

// readLine reads one line. It returns the line without its newline when it is
// within maxLine. For a longer line it returns only the first longLinePrefix
// bytes and the full size, and reads on to the newline without keeping the rest.
// The error is io.EOF at the end of the stream, after which line may still hold a
// final line with no newline.
func readLine(br *bufio.Reader) (line []byte, size int, err error) {
	var buf []byte
	long := false
	for {
		chunk, rerr := br.ReadSlice('\n')
		size += len(chunk)
		if !long {
			if size > maxLine {
				long = true
				buf = append(buf[:0], buf[:min(len(buf), longLinePrefix)]...)
			} else {
				buf = append(buf, chunk...)
			}
		}
		if errors.Is(rerr, bufio.ErrBufferFull) {
			continue
		}
		buf = bytes.TrimRight(buf, "\r\n")
		if long {
			return buf[:min(len(buf), longLinePrefix)], size, rerr
		}
		return buf, 0, rerr
	}
}

// handleInput handles one line. size is 0 for a line read whole, and the full
// length for an over-long one, whose line holds only a prefix.
func (p *Parser) handleInput(line []byte, size int, emit func(events.Event)) {
	if size > 0 {
		p.flushWatchdog(emit)
		p.sawJSON = true
		emit(p.newEvent(events.KindError, events.ErrorData{
			Kind:    events.ErrKindParse,
			Message: fmt.Sprintf("line over the size limit (%d bytes): %s", size, strings.ToValidUTF8(string(line), "?")),
		}, nil))
		return
	}
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}
	p.flushWatchdog(emit)
	if line[0] != '{' {
		if p.sawJSON {
			emit(p.newEvent(events.KindError, events.ErrorData{
				Kind:    events.ErrKindParse,
				Message: "line is not a json object: " + events.TruncateString(string(line), events.MaxSummaryBytes),
			}, nil))
		}
		return
	}
	p.sawJSON = true
	p.handleLine(line, emit)
}

// flushWatchdog reports a held watchdog line that turned out not to be last.
func (p *Parser) flushWatchdog(emit func(events.Event)) {
	if p.watchdog == nil {
		return
	}
	w := p.watchdog
	p.watchdog = nil
	emit(p.unmapped("marvel.watchdog line is not the last line of the stream", w.raw))
}

// finishWatchdog ends the session for a watchdog line that was the last line. A
// run that already completed is not ended twice.
func (p *Parser) finishWatchdog(emit func(events.Event)) {
	if p.watchdog == nil {
		return
	}
	w := p.watchdog
	p.watchdog = nil
	if p.completed {
		emit(p.unmapped("marvel.watchdog line after complete", w.raw))
		return
	}
	data := events.SessionEndedData{
		Reason:   "watchdog: " + w.reason,
		ExitCode: watchdogExit,
	}
	if len(p.denied) > 0 {
		data.Metering = &events.Metering{PermissionDenials: append([]events.PermissionDenial(nil), p.denied...)}
	}
	emit(p.newEvent(events.KindSessionEnded, data, nil))
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
	case WatchdogType:
		var w struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(raw, &w)
		if w.Reason != "idle" && w.Reason != "wall-clock" {
			emit(p.unmapped("marvel.watchdog line without a known reason", raw))
			return
		}
		p.watchdog = &heldWatchdog{reason: w.Reason, raw: raw}
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
	p.completed = true
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
