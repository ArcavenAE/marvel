package daemon

import (
	"encoding/json"

	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// The wire format and the client live in internal/daemonrpc. The names below
// carry the move: a type alias for each moved type and a one-line forwarder
// for each moved function, so a caller that still says daemon.Request or
// daemon.SendRequest builds unchanged. Both sets go when the callers are
// rewritten (docs/design/code-restructure-plan.md, Move 1).

type (
	Request     = daemonrpc.Request
	Response    = daemonrpc.Response
	EventsBatch = daemonrpc.EventsBatch
	DialOptions = daemonrpc.DialOptions
)

// MethodEventsWatch is defined in daemonrpc.
const MethodEventsWatch = daemonrpc.MethodEventsWatch

// ErrWatchUnsupported is the one error value daemonrpc returns, so errors.Is
// matches through either name.
var ErrWatchUnsupported = daemonrpc.ErrWatchUnsupported

// SendRequest forwards to daemonrpc.SendRequest.
func SendRequest(socketPath string, req Request) (*Response, error) {
	return daemonrpc.SendRequest(socketPath, req)
}

// SendRequestWith forwards to daemonrpc.SendRequestWith.
func SendRequestWith(socketPath string, req Request, opts DialOptions) (*Response, error) {
	return daemonrpc.SendRequestWith(socketPath, req, opts)
}

// WatchEventsWith forwards to daemonrpc.WatchEventsWith.
func WatchEventsWith(socketPath string, params json.RawMessage, opts DialOptions, fn func(*Response, EventsBatch) error) error {
	return daemonrpc.WatchEventsWith(socketPath, params, opts, fn)
}

// listenNetwork, isMRVL and isSSH forward to the exported helpers in daemonrpc.
func listenNetwork(addr string) string { return daemonrpc.ListenNetwork(addr) }
func isMRVL(addr string) bool          { return daemonrpc.IsMRVL(addr) }
func isSSH(addr string) bool           { return daemonrpc.IsSSH(addr) }
