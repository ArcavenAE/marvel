// Package codex implements the marvel runtime adapter parse layer for the
// Codex CLI invoked with `codex exec --json`. It translates codex's JSONL
// event stream into the shared marvel event vocabulary, mirroring the
// shape of internal/runtime/claudecode.
//
// Verified against codex-cli 0.146.0 fixtures (see testdata and
// mapping.md). The event dialect changed across codex versions; this
// parser targets the 0.146 `thread.started` / `turn.*` / `item.*` shape,
// not the older `config` / `msg` shape.
package codex

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
const Harness = "codex"

// Config carries the marvel-side context every emitted event needs.
// AgentID and Workspace are stamped on every event; HarnessSessionID is
// learned from the thread.started line and applied thereafter. Clock
// defaults to time.Now().UTC() when nil — override for tests.
type Config struct {
	AgentID   string
	Workspace string
	Clock     func() time.Time
}

// Parser translates the codex `exec --json` JSONL stream into normalized
// events. A Parser instance corresponds to one stream; construct a new
// one per session.
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

// Parse consumes JSONL from r, one codex event per line, until r returns
// io.EOF or an unrecoverable read error. A malformed line emits
// error{kind:"parse"} and parsing continues; an unknown event type emits
// error{kind:"unmapped"} with the raw line preserved. ctx cancellation is
// polled between lines.
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

func (p *Parser) handleLine(line []byte, emit func(events.Event)) {
	raw := make(json.RawMessage, len(line))
	copy(raw, line)

	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		emit(p.newEvent(events.KindError, events.ErrorData{
			Kind:    events.ErrKindParse,
			Message: fmt.Sprintf("malformed json: %v", err),
		}, raw))
		return
	}

	switch head.Type {
	case "thread.started":
		p.handleThreadStarted(raw, emit)
	case "turn.started":
		emit(p.newEvent(events.KindTurnStarted, events.TurnData{}, nil))
	case "turn.completed":
		p.handleTurnCompleted(raw, emit)
	case "turn.failed":
		p.handleTurnFailed(raw, emit)
	case "item.started":
		p.handleItem(raw, true, emit)
	case "item.completed":
		p.handleItem(raw, false, emit)
	case "error":
		p.handleError(raw, emit)
	default:
		emit(p.newEvent(events.KindError, events.ErrorData{
			Kind:    events.ErrKindUnmapped,
			Message: fmt.Sprintf("unknown codex event type: %q", head.Type),
		}, raw))
	}
}

func (p *Parser) handleThreadStarted(raw json.RawMessage, emit func(events.Event)) {
	var body struct {
		ThreadID string `json:"thread_id"`
		Model    string `json:"model"`
		Cwd      string `json:"cwd"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		emit(p.newEvent(events.KindError, events.ErrorData{
			Kind:    events.ErrKindParse,
			Message: fmt.Sprintf("thread.started body: %v", err),
		}, raw))
		return
	}
	if body.ThreadID != "" {
		p.sessID = body.ThreadID
	}
	emit(p.newEvent(events.KindSessionStarted, events.SessionStartedData{
		Model: body.Model,
		Cwd:   body.Cwd,
	}, nil))
}

// codexUsage mirrors the turn.completed usage object. Codex reports token
// counts but no cost; Usage.Cost therefore stays nil for this harness.
type codexUsage struct {
	InputTokens           int `json:"input_tokens"`
	CachedInputTokens     int `json:"cached_input_tokens"`
	CacheWriteInputTokens int `json:"cache_write_input_tokens"`
	OutputTokens          int `json:"output_tokens"`
	ReasoningOutputTokens int `json:"reasoning_output_tokens"`
}

func (p *Parser) handleTurnCompleted(raw json.RawMessage, emit func(events.Event)) {
	var body struct {
		Usage codexUsage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		emit(p.newEvent(events.KindError, events.ErrorData{
			Kind:    events.ErrKindParse,
			Message: fmt.Sprintf("turn.completed body: %v", err),
		}, raw))
		return
	}
	emit(p.newEvent(events.KindTurnCompleted, events.TurnData{
		UsageDelta: events.Usage{
			In:  body.Usage.InputTokens,
			Out: body.Usage.OutputTokens,
		},
	}, nil))
}

func (p *Parser) handleTurnFailed(raw json.RawMessage, emit func(events.Event)) {
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &body)
	msg := body.Error.Message
	if msg == "" {
		msg = "turn failed"
	}
	emit(p.newEvent(events.KindError, events.ErrorData{
		Kind:    "vendor",
		Message: msg,
	}, raw))
}

// codexItem is the item payload on item.started / item.completed lines.
// Only fields the parser routes on are declared; the rest stays in raw.
type codexItem struct {
	ID               string `json:"id"`
	Type             string `json:"type"`
	Text             string `json:"text"`
	Command          string `json:"command"`
	AggregatedOutput string `json:"aggregated_output"`
	ExitCode         *int   `json:"exit_code"`
	Status           string `json:"status"`
}

func (p *Parser) handleItem(raw json.RawMessage, started bool, emit func(events.Event)) {
	var body struct {
		Item codexItem `json:"item"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		emit(p.newEvent(events.KindError, events.ErrorData{
			Kind:    events.ErrKindParse,
			Message: fmt.Sprintf("item body: %v", err),
		}, raw))
		return
	}
	item := body.Item

	switch item.Type {
	case "command_execution":
		if started {
			emit(p.newEvent(events.KindToolCall, events.ToolCallData{
				Tool:   item.Type,
				CallID: item.ID,
				Input:  events.TruncateString(item.Command, events.MaxSummaryBytes),
			}, nil))
			return
		}
		emit(p.newEvent(events.KindToolResult, events.ToolResultData{
			CallID: item.ID,
			OK:     commandOK(item.Status, item.ExitCode),
			Output: events.TruncateString(item.AggregatedOutput, events.MaxSummaryBytes),
		}, nil))
	case "agent_message":
		// agent_message arrives only as item.completed; a started marker
		// (should it ever appear) carries no text and is skipped.
		if started {
			return
		}
		emit(p.newEvent(events.KindMessageCompleted, events.MessageData{
			Role: "assistant",
			Text: events.TruncateString(item.Text, events.MaxSummaryBytes),
		}, nil))
	case "reasoning":
		// Codex reasoning items are the thinking analog and have no v1
		// event kind. Per the never-drop rule, emit unmapped with raw.
		emit(p.newEvent(events.KindError, events.ErrorData{
			Kind:    events.ErrKindUnmapped,
			Message: "codex reasoning item (no v1 event kind)",
		}, raw))
	default:
		// mcp_tool_call, web_search, file_change, todo_list and any other
		// item type: shape unverified against a fixture, so preserve raw
		// rather than guess a mapping. See mapping.md.
		emit(p.newEvent(events.KindError, events.ErrorData{
			Kind:    events.ErrKindUnmapped,
			Message: fmt.Sprintf("unmapped codex item type: %q", item.Type),
		}, raw))
	}
}

// commandOK reports whether a command_execution item succeeded. A missing
// exit_code (in-progress items carry null) is not a success; a present
// non-zero code is a failure regardless of status.
func commandOK(status string, exitCode *int) bool {
	if status != "completed" {
		return false
	}
	return exitCode != nil && *exitCode == 0
}

func (p *Parser) handleError(raw json.RawMessage, emit func(events.Event)) {
	var body struct {
		Message string `json:"message"`
		Error   struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &body)
	msg := body.Message
	if msg == "" {
		msg = body.Error.Message
	}
	emit(p.newEvent(events.KindError, events.ErrorData{
		Kind:    "vendor",
		Message: msg,
	}, raw))
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
