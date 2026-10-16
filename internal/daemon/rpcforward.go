package daemon

import "github.com/arcavenae/marvel/internal/daemonrpc"

// The wire format and the client live in internal/daemonrpc. The names below
// carry the move: a type alias for each moved type and a one-line forwarder
// for each moved type, so a caller that still says daemon.Request builds
// unchanged. The client function forwarders went with the callers' rewrite (PR
// 1b); the aliases go in PR 1c (docs/design/code-restructure-plan.md, Move 1).

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

// listenNetwork, isMRVL and isSSH forward to the exported helpers in daemonrpc.
func listenNetwork(addr string) string { return daemonrpc.ListenNetwork(addr) }
func isMRVL(addr string) bool          { return daemonrpc.IsMRVL(addr) }
func isSSH(addr string) bool           { return daemonrpc.IsSSH(addr) }
