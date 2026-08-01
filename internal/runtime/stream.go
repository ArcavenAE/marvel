package runtime

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/arcavenae/marvel/internal/runtime/claudecode"
	"github.com/arcavenae/marvel/internal/runtime/codex"
	"github.com/arcavenae/marvel/internal/runtime/events"
	"github.com/arcavenae/marvel/internal/runtime/opencode"
)

// StreamFormat names the wire format a harness writes to the sink
// marvel hands it. It is the only thing the session manager needs to
// know about a harness's telemetry — the manager creates a sink,
// passes the path down, and asks for a parser by format. Adding a
// harness means adding a format and a parser constructor, not
// teaching the manager anything new.
type StreamFormat string

// StreamFormatClaudeCodeJSON is the NDJSON stream Claude Code writes
// under `--output-format stream-json --verbose`.
const StreamFormatClaudeCodeJSON StreamFormat = "claude-code/stream-json"

// StreamFormatCodexJSON is the JSONL stream Codex writes under
// `codex exec --json`.
const StreamFormatCodexJSON StreamFormat = "codex/jsonl"

// StreamFormatOpenCodeJSON is the line-delimited JSON stream OpenCode
// writes under `opencode run --format json`.
const StreamFormatOpenCodeJSON StreamFormat = "opencode/json"

// StreamSpec is an adapter's report that it wired its harness's
// structured output to Path. The adapter fills this in only when the
// LaunchContext carried a StreamPath it could use.
type StreamSpec struct {
	Format StreamFormat
	Path   string
}

// StreamCapable is the optional capability an Adapter implements when
// it can redirect its harness's structured output to a marvel-provided
// sink. The session manager calls SupportsStream before doing any
// setup work, so adapters that never stream (and adapters asked to
// launch an interactive session) cost nothing.
//
// SupportsStream is called with a LaunchContext whose StreamPath is
// still empty: the answer must depend on the runtime and role, not on
// the sink.
type StreamCapable interface {
	SupportsStream(ctx *LaunchContext) bool
}

// StreamParser consumes a harness's telemetry stream and emits
// normalized events until r hits EOF or ctx is cancelled.
type StreamParser interface {
	Parse(ctx context.Context, r io.Reader, emit func(events.Event)) error
}

// StreamParserConfig is the marvel-side identity stamped onto every
// event a parser emits. Clock is for tests; nil means wall clock.
type StreamParserConfig struct {
	AgentID   string
	Workspace string
	Clock     func() time.Time
}

// NewStreamParser returns the parser for a stream format. Unknown
// formats are an error rather than a silent no-op — an adapter that
// declares a format marvel cannot read is a wiring bug, and the
// session should surface it instead of running blind.
func NewStreamParser(format StreamFormat, cfg StreamParserConfig) (StreamParser, error) {
	switch format {
	case StreamFormatClaudeCodeJSON:
		return claudecode.NewParser(claudecode.Config{
			AgentID:   cfg.AgentID,
			Workspace: cfg.Workspace,
			Clock:     cfg.Clock,
		}), nil
	case StreamFormatCodexJSON:
		return codex.NewParser(codex.Config{
			AgentID:   cfg.AgentID,
			Workspace: cfg.Workspace,
			Clock:     cfg.Clock,
		}), nil
	case StreamFormatOpenCodeJSON:
		return opencode.NewParser(opencode.Config{
			AgentID:   cfg.AgentID,
			Workspace: cfg.Workspace,
			Clock:     cfg.Clock,
		}), nil
	default:
		return nil, fmt.Errorf("no parser for stream format %q", format)
	}
}
