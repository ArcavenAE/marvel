// Package daemonrpc is the marvel daemon's wire format and its client: the
// request and response envelopes, the method names, and the functions that
// dial a daemon over a Unix socket, mrvl://, ssh:// or bare TCP and carry one
// request or one events.watch stream. The daemon package consumes the types;
// the CLI and the simulator reach a daemon through the client. The shape
// follows net/rpc.
package daemonrpc

import (
	"encoding/json"

	"github.com/arcavenae/marvel/internal/events"
)

// MethodVersion is the read-only RPC that reports the running daemon's build.
// It answers from the daemon's own memory and never touches the state store,
// which this process holds under an exclusive bbolt lock.
const MethodVersion = "version"

// Request is a JSON-RPC-like request from the CLI.
type Request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC-like response to the CLI.
type Response struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`

	// DaemonHome is the layout home this daemon is rooted at
	// (~/.marvel), stamped on every response so a client can tell
	// whether the daemon it reached is the one it meant. Empty from a
	// daemon that predates the field, and omitempty keeps the envelope
	// readable by a client that predates it, so no protocol version bump
	// or handshake is involved.
	//
	// Diagnostic, not preventive. A field on the RESPONSE is read after
	// the request has already been sent: for read methods that is fine,
	// but for mutating methods the client learns it hit the wrong daemon
	// after it has already changed it. Prevention would put the
	// expectation on the REQUEST and have the daemon reject a mismatch,
	// which is authorization-shaped and belongs with aae-orc-sqh0. See
	// docs/design/daemon-isolation.md decision 8.
	DaemonHome string `json:"daemon_home,omitempty"`

	// DaemonBuild is the answering daemon's build, the JSON of daemon.Build,
	// stamped on every response beside DaemonHome so a client can warn on any
	// verb when its build differs from the daemon's
	// (docs/design/zero-downtime-upgrade.md section 6). Raw JSON because the
	// type lives in the daemon package, which imports this one. Empty from a
	// daemon that predates the field, and omitempty keeps the envelope
	// readable by a client that predates it. Diagnostic only, like DaemonHome.
	DaemonBuild json.RawMessage `json:"daemon_build,omitempty"`

	// Starting is true on every response the daemon sends while it is still
	// adopting tmux state, so a client reading get or describe knows the rows
	// may be incomplete. Absent once the daemon has finished starting.
	Starting bool `json:"starting,omitempty"`
}

// EventsBatch is the result of the events method and one message on an
// events.watch stream. Dropped and ResumeFrom are set only on a stream,
// and only when the daemon-side watch overflowed since the last message:
// Dropped events were never sent, and Snapshot with since_seq =
// ResumeFrom returns them, subject to the ring's capacity.
type EventsBatch struct {
	Events     []events.Event `json:"events"`
	Dropped    uint64         `json:"dropped,omitempty"`
	ResumeFrom uint64         `json:"resume_from,omitempty"`
}

// MethodEventsWatch is the streaming sibling of the events method. The
// request carries the same params; the daemon answers with one
// EventsBatch for the backlog (events after since_seq, or the last n
// when there is no cursor) and then one per delivery for the life of
// the connection. The client ends the stream by closing its side.
const MethodEventsWatch = "events.watch"
