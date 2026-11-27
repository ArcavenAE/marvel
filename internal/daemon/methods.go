package daemon

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// methodSpec is everything the daemon knows about one RPC method. The table
// below is the one place a method is declared: the dispatch, the methods that
// answer while the daemon is adopting, the set a credential-push key may call,
// the local-socket-only set, the set a fan-out may call and the set the timing
// buckets name are all derived from it (I-458 row 6). A method added here and
// missing a column fails TestMethodTableSets, not a request at run time.
type methodSpec struct {
	// Handle serves the method. params is the request's params as sent, and c
	// is the caller the transport authenticated.
	Handle func(d *Daemon, params json.RawMessage, c caller) daemonrpc.Response
	// Starting is true when the method answers while the daemon is still
	// adopting tmux state, or held by a suspend (see startingAllows).
	Starting bool
	// Deferred is true for a report the seat can retry: while the daemon is
	// adopting it is answered ErrReportDeferred, not ErrDaemonStarting.
	Deferred bool
	// CredentialPush is true when a credential-push key may call the method.
	CredentialPush bool
	// LocalOnly is true when only the daemon's own unix socket may call it,
	// never the mrvl:// tunnel, whatever scope a tunnelled key carries.
	LocalOnly bool
	// FanOut is true when a fan-out across clusters may call the method: reads
	// only.
	FanOut bool
	// Streams is true for a method that answers with many responses on one
	// connection; handleRWCAs routes it before dispatch.
	Streams bool
}

// methodTable maps a method name to its spec. It is filled once in init, so
// the handlers it names may call back into dispatch without an initialization
// cycle.
var methodTable map[string]methodSpec

func init() {
	methodTable = map[string]methodSpec{
		daemonrpc.MethodEventsWatch: {
			// Reachable only through dispatch(), which tests and the SSH path
			// do not use for this method; handleRWCAs routes it to the
			// streaming handler before dispatch.
			Handle: func(d *Daemon, p json.RawMessage, c caller) daemonrpc.Response {
				return daemonrpc.Response{Error: fmt.Sprintf("%s streams many responses on one connection; use daemonrpc.WatchEventsWith", daemonrpc.MethodEventsWatch)}
			},
			Streams: true,
		},
		daemonrpc.MethodAccountDelta: {
			Handle: func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleAccountDelta(p) },
		},
		daemonrpc.MethodAccountLimits: {
			Handle:   func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleAccountLimits(p) },
			Deferred: true,
		},
		daemonrpc.MethodAccountSeries: {
			Handle: func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleAccountSeries(p) },
		},
		daemonrpc.MethodApply: {
			Handle: func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleApply(p) },
		},
		daemonrpc.MethodBackendVerify: {
			Handle: func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleBackendVerify(p) },
		},
		// broker.token returns a bearer token, so like a revealed credential it never
		// leaves through the mrvl:// tunnel.
		daemonrpc.MethodBrokerToken: {
			Handle: func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response {
				return d.handleBrokerToken(context.Background(), p)
			},
			LocalOnly: true,
		},
		daemonrpc.MethodBusAudit: {
			Handle:   func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleBusAudit(p) },
			Starting: true,
		},
		daemonrpc.MethodBusBeat: {
			Handle:   func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleBusBeat(p) },
			Deferred: true,
		},
		// bus.leaf.connect and bus.leaf.disconnect reuse the credential-push gate rather
		// than growing a new scope: the leaf toggle is an operational sibling of the
		// enrollment a credential-push key already performs, and a dedicated grant waits
		// on the principal model (aae-orc-bs3x). They are the leaf lifecycle's
		// operational verbs, not credential reads, so they converge on the operator or
		// supervisor principal when bs3x lands (aae-orc-ct0l4).
		daemonrpc.MethodBusLeafConnect: {
			Handle:         func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleBusLeafConnect() },
			CredentialPush: true,
		},
		daemonrpc.MethodBusLeafDisconnect: {
			Handle:         func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleBusLeafDisconnect() },
			CredentialPush: true,
		},
		daemonrpc.MethodBusStatus: {
			Handle:   func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleBusStatus() },
			Starting: true,
		},
		daemonrpc.MethodCapture: {
			Handle: func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleCapture(p) },
		},
		daemonrpc.MethodConverge: {
			Handle: func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleConverge(p) },
		},
		daemonrpc.MethodCredentialDelete: {
			Handle:         func(d *Daemon, p json.RawMessage, c caller) daemonrpc.Response { return d.handleCredentialDelete(p, c) },
			CredentialPush: true,
		},
		// credential.get is deliberately not a credential-push method: a pushing key never
		// reads a value back (reveal is local-socket only, S3).
		daemonrpc.MethodCredentialGet: {
			Handle: func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleCredentialGet(p) },
		},
		daemonrpc.MethodCredentialList: {
			Handle:         func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleCredentialList() },
			Starting:       true,
			CredentialPush: true,
		},
		daemonrpc.MethodCredentialPut: {
			Handle:         func(d *Daemon, p json.RawMessage, c caller) daemonrpc.Response { return d.handleCredentialPut(p, c) },
			CredentialPush: true,
		},
		// credential.reveal returns a secret value, so it is confined to the operator at the
		// daemon's own socket (brief 9 S3), never the mrvl:// tunnel.
		daemonrpc.MethodCredentialReveal: {
			Handle:    func(d *Daemon, p json.RawMessage, c caller) daemonrpc.Response { return d.handleCredentialReveal(p, c) },
			LocalOnly: true,
		},
		daemonrpc.MethodDaemonStatus: {
			Handle:   func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleDaemonStatus() },
			Starting: true,
		},
		daemonrpc.MethodHostVitals: {
			Handle:   func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleHostVitals() },
			Starting: true,
			FanOut:   true,
		},
		daemonrpc.MethodDelete: {
			Handle: func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleDelete(p) },
		},
		daemonrpc.MethodDescribe: {
			Handle:   func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleDescribe(p) },
			Starting: true,
		},
		daemonrpc.MethodEvents: {
			Handle:   func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleEvents(p) },
			Starting: true,
		},
		daemonrpc.MethodExposureAdd: {
			Handle: func(d *Daemon, p json.RawMessage, c caller) daemonrpc.Response { return d.handleExposureAdd(p, c) },
		},
		daemonrpc.MethodExposureEnd: {
			Handle: func(d *Daemon, p json.RawMessage, c caller) daemonrpc.Response { return d.handleExposureEnd(p, c) },
		},
		// exposure.void releases a refusal, so it is served only on the daemon's own
		// socket and never through the tunnel (marvel#945). That checks where the caller
		// is, not who: every seat on the host holds that socket, so today any local seat
		// can void. Limiting it to the operator needs a principal check that does not
		// exist yet.
		daemonrpc.MethodExposureVoid: {
			Handle:    func(d *Daemon, p json.RawMessage, c caller) daemonrpc.Response { return d.handleExposureVoid(p, c) },
			LocalOnly: true,
		},
		daemonrpc.MethodGet: {
			Handle:   func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleGet(p) },
			Starting: true,
			FanOut:   true,
		},
		// handoff.put writes a file on the daemon host on a seat's behalf. The heartbeat
		// token alone decides whose handoff it is, and every seat holds the local socket,
		// so this is the narrowest grant: not the tunnel, and not a credential-push key
		// (docs/design/read-only-seat-handoff.md, section 2).
		daemonrpc.MethodHandoffPut: {
			Handle:    func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleHandoffPut(p) },
			LocalOnly: true,
		},
		// run.sandboxed starts a command for a seat under a profile the daemon
		// composes. The token decides which seat, so it is local-socket only and
		// never a credential-push call (marvel#1596).
		daemonrpc.MethodRunSandboxed: {
			Handle: func(d *Daemon, p json.RawMessage, c caller) daemonrpc.Response {
				ctx := c.ctx
				if ctx == nil {
					ctx = context.Background()
				}
				return d.handleRunSandboxed(ctx, p)
			},
			LocalOnly: true,
		},
		daemonrpc.MethodHeartbeat: {
			Handle:   func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleHeartbeat(p) },
			Deferred: true,
		},
		daemonrpc.MethodInject: {
			Handle: func(d *Daemon, p json.RawMessage, c caller) daemonrpc.Response { return d.handleInjectAs(p, c) },
		},
		daemonrpc.MethodLogs: {
			Handle:   func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleLogs(p) },
			Starting: true,
		},
		daemonrpc.MethodOrphans: {
			Handle:   func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleOrphans() },
			Starting: true,
		},
		daemonrpc.MethodPlan: {
			Handle: func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handlePlan() },
		},
		daemonrpc.MethodPrunePlan: {
			Handle: func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handlePrunePlan(p) },
		},
		daemonrpc.MethodReap: {
			Handle: func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleReap(p) },
		},
		daemonrpc.MethodReexec: {
			Handle: func(d *Daemon, p json.RawMessage, c caller) daemonrpc.Response { return d.handleReexec(p, c) },
		},
		daemonrpc.MethodResetHealth: {
			Handle: func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleResetHealth(p) },
		},
		daemonrpc.MethodResume: {
			Handle:    func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleResume() },
			Starting:  true,
			LocalOnly: true,
		},
		daemonrpc.MethodRun: {
			Handle: func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleRun(p) },
		},
		daemonrpc.MethodScale: {
			Handle: func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleScale(p) },
		},
		daemonrpc.MethodShift: {
			Handle: func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleShift(p) },
		},
		daemonrpc.MethodStop: {
			Handle: func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleStop(p) },
		},
		// suspend and resume freeze and resume the whole fleet. Until the suspend scope
		// exists (docs/design/fleet-suspend-resume.md section 6), neither is served
		// through the tunnel.
		daemonrpc.MethodSuspend: {
			Handle:    func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleSuspend(p) },
			LocalOnly: true,
		},
		daemonrpc.MethodSuspendStatus: {
			Handle:   func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleSuspendStatus() },
			Starting: true,
		},
		// team.hibernate and team.wake are local only until the suspend scope exists, like
		// suspend and resume (docs/design/fleet-hibernate.md section 10 holds the
		// remote-scope decision).
		daemonrpc.MethodTeamHibernate: {
			Handle:    func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleHibernate(p) },
			LocalOnly: true,
		},
		daemonrpc.MethodTeamWake: {
			Handle:    func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleWake(p) },
			LocalOnly: true,
		},
		daemonrpc.MethodVersion: {
			Handle:   func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleVersion() },
			Starting: true,
			FanOut:   true,
		},
		daemonrpc.MethodViewRefresh: {
			Handle: func(d *Daemon, p json.RawMessage, _ caller) daemonrpc.Response { return d.handleViewRefresh(p) },
		},
	}
	deriveMethodSets()
}
