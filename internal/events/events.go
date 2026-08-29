// Package events provides a bounded in-memory ring of structured
// state-transition events — marvel's equivalent of `kubectl get events`.
// Complements internal/logbuf (raw daemon stderr stream) with
// queryable, filterable history keyed to sessions, teams, and
// workspaces.
//
// The ring is the primary data structure. An Emitter interface lets
// producers (session.Manager, team.Controller) emit without coupling
// to the ring type; tests can inject a DiscardEmitter.
package events

import (
	"sync"
	"time"
)

// Kind identifies a class of event. These are stable string tags —
// clients can filter on them, dashboards can group on them.
type Kind string

// Canonical event kinds. New producers should add entries here rather
// than inventing string literals at call sites.
const (
	KindSessionCreated    Kind = "session.created"
	KindSessionDeleted    Kind = "session.deleted"
	KindSessionCrashed    Kind = "session.crashed"
	KindSessionRestarted  Kind = "session.restarted"
	KindSessionFailed     Kind = "session.failed"
	KindHealthCheckFailed Kind = "health.failed"
	KindCrashLoopBackoff  Kind = "health.crashloop-backoff"
	KindShiftStarted      Kind = "team.shift-started"
	KindShiftCompleted    Kind = "team.shift-completed"
	KindShiftTimedOut     Kind = "team.shift-timed-out"
	// KindShiftRoleReady records the instant the control plane decided a
	// role's successor generation may take over: allReady returned true and
	// the shift advanced from launching to draining. It is the boundary
	// between the two stages of a shift, and the only stage boundary that
	// will move when real harnesses replace simulators, so it is the stamp
	// an automatic shift trigger has to reason about.
	//
	// The subject is the ROLE's successor generation, not one session,
	// because allReady is a group gate over every replica. Session is
	// therefore left empty even for a single-replica role: a field that is
	// populated only at replicas=1 would let a consumer join by session key
	// and silently lose the multi-replica rows. The successor keys and the
	// gate that admitted them ride in Message; Generation carries the
	// successor generation as data.
	//
	// Fires at most once per role per shift: the phase flip that emits it is
	// also the flip that stops allReady being consulted for that role. See
	// orc finding-018, which had to poll the session table at 50 Hz to
	// timestamp this instant because the ring did not carry it.
	KindShiftRoleReady Kind = "team.shift-role-ready"
	// KindShiftDrainedEmpty records that a shift advanced past a role whose
	// old generation was already empty when draining began — it drained
	// nothing. An empty old generation is ambiguous: it is both the state
	// after every predecessor has drained and the state when there was
	// never anything to drain (a mis-tagged or early-deleted generation, or
	// an intentional 0→N scale-up). The shift advances either way — this is
	// surface, not judge — but the event lets an operator or a test tell the
	// two apart, which len(oldGen)==0 alone could not. See aae-orc-094e.
	KindShiftDrainedEmpty Kind = "team.shift-drained-empty"
	KindRoleSaturated     Kind = "role.saturated"
	KindRoleRemoved       Kind = "role.removed"
	// KindPolicyProjected records that marvel wrote (or rewrote) a
	// session's projected Claude Code settings file — the observable
	// signal of a policy landing at spawn and of live re-projection after
	// a manifest change. See finding-024.
	KindPolicyProjected Kind = "policy.projected"
	// KindContextLimitUnresolved records that marvel measured a session's
	// context tokens but could not resolve the model's context window, so
	// CTX% stays blank rather than showing a percentage against a guessed
	// denominator. Fires once per session. This is the operator's answer
	// to "why is that column empty", and the fix is usually one
	// runtime.context_window line in the manifest.
	KindContextLimitUnresolved Kind = "context.limit-unresolved"
	// KindAdmissionRefused records that marvel refused to spawn against a
	// team-declared budget, with the arithmetic in the Message. Fires at
	// every refusal point: the operator's verb (apply, scale, run, shift)
	// and the reconciler backstop. Edge-triggered on the verdict in the
	// reconciler, one per operator action at a verb. See aae-orc-qiay.
	KindAdmissionRefused Kind = "admission.refused"
	// KindAdmissionCleared records that a standing admission refusal stopped
	// applying, so a role held back by a budget may grow again.
	KindAdmissionCleared Kind = "admission.cleared"
	// KindAdmissionUnmeasured records that a declared clause was admitted
	// against a total the meter could not supply. The operator declared a
	// ceiling on a dimension, not "refuse when unmeasurable", so the default
	// admits — audibly, here — and budget.on_unmeasured = "refuse" is how
	// they ratify the fail-closed posture instead.
	KindAdmissionUnmeasured Kind = "admission.unmeasured"
	// KindReconcileAdopted records that a daemon claimed a live tmux pane
	// it found matching its own recorded intent at startup.
	KindReconcileAdopted Kind = "reconcile.adopted"
	// KindReconcileKilled records that a daemon destroyed a marvel-* tmux
	// entity it did not find in its own records. This is the most
	// destructive act marvel performs and until aae-orc-kvcs it was the
	// only state transition in session.Manager with no event: the log
	// carried it, the ring did not, so `marvel events` was blank for a
	// fleet kill. Warning severity, and Actor is always set, because the
	// killed entity may belong to a second daemon that records nothing
	// itself.
	KindReconcileKilled Kind = "reconcile.killed"
	// KindHeartbeatRefused records that a heartbeat claimed a session it
	// was not issued for: it presented no token, or the wrong one, for a
	// session marvel minted a token for. Warning severity, and it names
	// the claimed session, because the interesting case is not a broken
	// agent reporting itself but one process reporting on another's
	// behalf. LastHeartbeat feeds the healthcheck, the restart policy,
	// and shift readiness, so a forged one keeps a dead peer looking
	// healthy for as long as it keeps sending.
	KindHeartbeatRefused Kind = "heartbeat.refused"
	// KindHeartbeatUnbound records that a heartbeat was admitted against
	// a session record carrying no token hash: a session spawned before
	// marvel minted tokens, still beating across the upgrade. Warning
	// severity: the exemption drains as those sessions end, and an
	// accepted gap nobody can see is not an accepted gap.
	KindHeartbeatUnbound Kind = "heartbeat.unbound"
	// KindReconcileLeft records that a daemon found marvel-* tmux state
	// it does not own and left it running, which is the default posture
	// ratified 2026-08-07.
	//
	// Warning severity on purpose. The ruling accepted accumulating
	// orphans as the better of two failures, and an accepted failure that
	// nobody can see is just the other one wearing a different hat. This
	// is the event that keeps "leave it alone" from being as silent as
	// the kill it replaced.
	KindReconcileLeft Kind = "reconcile.left"
)

// Agent-stream kinds. These are the runtime adapter vocabulary
// (internal/runtime/events, twelve kinds) lifted into the ring, one ring
// kind per adapter kind, prefixed so `marvel events --kind
// agent.tool.call` reads next to the control-plane kinds above. The
// prefix also keeps the two namespaces from colliding as either grows.
//
// The kinds above report what marvel did to a session; these report what
// the agent inside it did.
const (
	KindAgentSessionStarted      Kind = "agent.session.started"
	KindAgentSessionEnded        Kind = "agent.session.ended"
	KindAgentTurnStarted         Kind = "agent.turn.started"
	KindAgentTurnCompleted       Kind = "agent.turn.completed"
	KindAgentMessageDelta        Kind = "agent.message.delta"
	KindAgentMessageCompleted    Kind = "agent.message.completed"
	KindAgentToolCall            Kind = "agent.tool.call"
	KindAgentToolResult          Kind = "agent.tool.result"
	KindAgentPermissionRequested Kind = "agent.permission.requested"
	KindAgentAuthRequired        Kind = "agent.auth.required"
	KindAgentHealthHeartbeat     Kind = "agent.health.heartbeat"
	KindAgentError               Kind = "agent.error"
)

// allKinds is every kind the ring can carry, in declaration order:
// control-plane first, agent stream second. It backs `marvel events
// --list-kinds`, which exists because `--kind` on a name that does not
// exist returns an empty result rather than an error, so a typo and "this
// never happened" are the same output.
//
// A hand-maintained list drifts. TestAllKindsCoversEveryDeclaredConstant
// parses this file and fails when a declared Kind constant is missing
// here, so the catalog cannot fall behind the constants it describes.
var allKinds = []Kind{
	KindSessionCreated,
	KindSessionDeleted,
	KindSessionCrashed,
	KindSessionRestarted,
	KindSessionFailed,
	KindHealthCheckFailed,
	KindCrashLoopBackoff,
	KindShiftStarted,
	KindShiftCompleted,
	KindShiftTimedOut,
	KindShiftRoleReady,
	KindShiftDrainedEmpty,
	KindRoleSaturated,
	KindRoleRemoved,
	KindPolicyProjected,
	KindContextLimitUnresolved,
	KindAdmissionRefused,
	KindAdmissionCleared,
	KindAdmissionUnmeasured,
	KindReconcileAdopted,
	KindReconcileKilled,
	KindHeartbeatRefused,
	KindHeartbeatUnbound,
	KindReconcileLeft,
	KindAgentSessionStarted,
	KindAgentSessionEnded,
	KindAgentTurnStarted,
	KindAgentTurnCompleted,
	KindAgentMessageDelta,
	KindAgentMessageCompleted,
	KindAgentToolCall,
	KindAgentToolResult,
	KindAgentPermissionRequested,
	KindAgentAuthRequired,
	KindAgentHealthHeartbeat,
	KindAgentError,
}

// AllKinds returns every kind the ring can carry, in declaration order.
// The returned slice is a copy: the catalog is read by a CLI command, and
// a caller sorting it in place would reorder it for everyone.
func AllKinds() []Kind {
	out := make([]Kind, len(allKinds))
	copy(out, allKinds)
	return out
}

// IsKnownKind reports whether name is a kind this build declares.
//
// Callers use it to tell an empty filter result apart from a misspelled
// filter. It is deliberately not wired into `--kind` as a hard rejection:
// a client can be older than the daemon it talks to, and refusing a kind
// the daemon knows about would turn version skew into a broken command.
func IsKnownKind(name string) bool {
	for _, k := range allKinds {
		if string(k) == name {
			return true
		}
	}
	return false
}

// Severity mirrors the kubernetes Warning/Normal distinction. Lets
// operators filter `marvel events --severity warning` for the things
// that need attention.
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
)

// Event is one structured state-transition record.
type Event struct {
	// Seq is a ring-assigned monotonic sequence number, starting at 1.
	// It exists so poll-based consumers (`marvel events --follow`) can
	// resume from the last event they saw instead of deduplicating on
	// timestamps, which are not unique. Producers never set it; the
	// ring assigns it under its own lock at Emit time.
	Seq       uint64    `json:"seq,omitempty"`
	Timestamp time.Time `json:"ts"`
	Kind      Kind      `json:"kind"`
	Severity  Severity  `json:"severity"`
	Workspace string    `json:"workspace,omitempty"`
	Team      string    `json:"team,omitempty"`
	Role      string    `json:"role,omitempty"`
	Session   string    `json:"session,omitempty"`
	// Actor names the daemon process that took the action, as
	// "pid=N socket=PATH". Empty on events whose actor is unambiguous.
	//
	// It exists because two daemons on one host append to the same
	// ~/.marvel/log/daemon.log by default, so their lines interleave with
	// nothing to tell them apart, and because the daemon whose work is
	// destroyed records nothing at all. Set it on any event describing an
	// action one daemon takes against state another daemon may own. See
	// aae-orc-kvcs.
	Actor string `json:"actor,omitempty"`
	// Generation names the team generation the event is about, when the
	// event is about one. Sessions already carry the generation in their
	// name (`<team>-<role>-g<gen>-<index>`), but an event whose subject is a
	// whole generation has nowhere to put it, which is how the shift
	// stage boundary in KindShiftRoleReady would otherwise have had to
	// encode its most load-bearing coordinate in prose. Zero means the
	// event is not generation-scoped.
	Generation int64 `json:"generation,omitempty"`
	// Message is a short human-readable description. Keep it one
	// line — operators scan dozens of these at a time.
	Message string `json:"message"`
}

// Emitter is what producers call to record an event. Nil is a safe
// value — callers use [Emit] which no-ops on a nil emitter.
type Emitter interface {
	Emit(Event)
}

// Emit is the producer-side sugar that handles nil emitters. Every
// caller in session.Manager / team.Controller goes through this so
// adding a new emission site is always safe regardless of whether
// the daemon wired the ring.
func Emit(e Emitter, ev Event) {
	if e == nil {
		return
	}
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	if ev.Severity == "" {
		ev.Severity = SeverityInfo
	}
	e.Emit(ev)
}

// Discard is an Emitter that drops events. Useful in tests that don't
// care to assert on the event stream.
type Discard struct{}

// Emit satisfies Emitter.
func (Discard) Emit(Event) {}

// Ring is a bounded in-memory event buffer. Safe for concurrent
// Emit / Snapshot calls.
type Ring struct {
	mu       sync.Mutex
	capacity int
	buf      []Event
	head     int // index of the oldest event when len(buf) == capacity
	full     bool
	nextSeq  uint64 // next Seq to assign; monotonic for the ring's lifetime
}

// DefaultCapacity is the ring size used when NewRing is called with
// a zero or negative capacity. Sized to cover a couple of hours of
// typical cluster activity without getting close to daemon RSS
// concerns — events are tiny compared to the log ring.
const DefaultCapacity = 2000

// NewRing returns a fresh ring with the given capacity. Zero or
// negative falls back to DefaultCapacity.
func NewRing(capacity int) *Ring {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Ring{
		capacity: capacity,
		buf:      make([]Event, 0, capacity),
		nextSeq:  1,
	}
}

// Emit satisfies Emitter. Appends to the tail, overwriting the head
// when full.
func (r *Ring) Emit(ev Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	if ev.Severity == "" {
		ev.Severity = SeverityInfo
	}
	ev.Seq = r.nextSeq
	r.nextSeq++
	if !r.full {
		r.buf = append(r.buf, ev)
		if len(r.buf) == r.capacity {
			r.full = true
		}
		return
	}
	r.buf[r.head] = ev
	r.head = (r.head + 1) % r.capacity
}

// Filter selects events to include in Snapshot results. Empty fields
// match anything; set fields must match exactly. For MinSeverity,
// SeverityWarning matches only warnings; the zero value matches all.
type Filter struct {
	Workspace   string
	Team        string
	Role        string
	Session     string
	Kind        Kind
	MinSeverity Severity
	// SinceSeq, when nonzero, matches only events with Seq strictly
	// greater than this value — the resume cursor for follow-mode
	// polling.
	SinceSeq uint64
}

// Snapshot returns up to `n` most recent events matching f, oldest-first
// (so the tail of the slice is the newest event). n<=0 returns all
// matching events.
func (r *Ring) Snapshot(f Filter, n int) []Event {
	r.mu.Lock()
	defer r.mu.Unlock()

	ordered := r.orderedLocked()
	var out []Event
	for _, ev := range ordered {
		if !matches(ev, f) {
			continue
		}
		out = append(out, ev)
	}
	if n > 0 && len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// Len returns the number of events currently stored.
func (r *Ring) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.buf)
}

// orderedLocked returns events oldest-first. Caller holds r.mu.
func (r *Ring) orderedLocked() []Event {
	if !r.full {
		out := make([]Event, len(r.buf))
		copy(out, r.buf)
		return out
	}
	out := make([]Event, r.capacity)
	copy(out, r.buf[r.head:])
	copy(out[r.capacity-r.head:], r.buf[:r.head])
	return out
}

func matches(ev Event, f Filter) bool {
	if f.Workspace != "" && f.Workspace != ev.Workspace {
		return false
	}
	if f.Team != "" && f.Team != ev.Team {
		return false
	}
	if f.Role != "" && f.Role != ev.Role {
		return false
	}
	if f.Session != "" && f.Session != ev.Session {
		return false
	}
	if f.Kind != "" && f.Kind != ev.Kind {
		return false
	}
	if f.MinSeverity == SeverityWarning && ev.Severity != SeverityWarning {
		return false
	}
	if f.SinceSeq > 0 && ev.Seq <= f.SinceSeq {
		return false
	}
	return true
}
