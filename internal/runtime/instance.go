package runtime

import (
	"context"

	"github.com/arcavenae/marvel/internal/runtime/events"
)

// Instance is the runtime-side seam: a running (or in-lifecycle) BYOA
// harness that marvel supervises. It complements Adapter, which
// constructs the launch recipe; an Instance is what you get once the
// launch has happened.
//
// Charter F9 lists four capabilities; the interface groups them so:
//
//  1. Construct env — belongs to Adapter.Prepare (existing seam).
//  2. Start/stop/restart — Spawn / Kill (restart is stop + fresh Spawn
//     of a new Instance; marvel does not preserve process identity
//     across restarts, only session identity).
//  3. Spawn/kill/inject/capture — Spawn, Kill, Inject, Capture below.
//  4. Report state — State + the Events channel, together.
//
// This is a scaffold. Concrete implementations land per-harness in
// sub-packages (internal/runtime/claudecode, /codex, /opencode). The
// scaffold intentionally omits process-management wiring (tmux driver,
// stdin/stdout attach, restart policy) — those live in the session
// manager and are stitched to Instance by later work.
type Instance interface {
	// Spawn starts the underlying harness. Non-blocking: it returns
	// once the process is launched (or start has demonstrably failed),
	// and callers observe subsequent behavior on Events. Spawn is not
	// safe to call twice on the same Instance — construct a new one.
	Spawn(ctx context.Context) error

	// Kill terminates the harness. Implementations should send a
	// graceful signal first (SIGTERM equivalent), waiting up to the
	// deadline in ctx before escalating. After Kill returns, Events
	// will close.
	Kill(ctx context.Context) error

	// Inject writes input to the harness — stdin for pipe-attached
	// processes, tmux send-keys for pane-attached ones. Adapters
	// declare which mechanism they use via their capabilities.
	Inject(input string) error

	// Capture returns a snapshot of recent harness output for
	// observation channels that don't produce a structured stream
	// (capture-pane fallback per charter F11). Adapters that emit
	// fully-normalized events on the Events channel may return an
	// empty snapshot and (nil, ErrCaptureUnsupported).
	Capture(ctx context.Context) ([]byte, error)

	// Events returns the read side of the normalized event stream.
	// The channel is closed when the harness exits or after Kill.
	// Consumers must drain it — a slow consumer blocks the parser
	// (bounded internal buffer applies back-pressure).
	Events() <-chan events.Event

	// State reports the current lifecycle state. Callers must not
	// treat State as a barrier — it is a report, not a lock.
	State() State
}

// State enumerates Instance lifecycle stages. The state machine is
// deliberately narrow: idle → spawning → running → stopping → stopped,
// with failed as a terminal alternative to stopped.
type State int

const (
	// StateIdle — constructed, not yet spawned.
	StateIdle State = iota
	// StateSpawning — Spawn was called and start is in progress.
	StateSpawning
	// StateRunning — harness is up; Events flowing.
	StateRunning
	// StateStopping — Kill was called; graceful shutdown in progress.
	StateStopping
	// StateStopped — harness terminated cleanly.
	StateStopped
	// StateFailed — spawn or run failed unrecoverably.
	StateFailed
)

// String returns a lower-case tag suitable for logging or the state
// field on a health.heartbeat event.
func (s State) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateSpawning:
		return "spawning"
	case StateRunning:
		return "running"
	case StateStopping:
		return "stopping"
	case StateStopped:
		return "stopped"
	case StateFailed:
		return "failed"
	default:
		return "unknown"
	}
}
