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
	KindRoleSaturated     Kind = "role.saturated"
)

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
	Timestamp time.Time `json:"ts"`
	Kind      Kind      `json:"kind"`
	Severity  Severity  `json:"severity"`
	Workspace string    `json:"workspace,omitempty"`
	Team      string    `json:"team,omitempty"`
	Role      string    `json:"role,omitempty"`
	Session   string    `json:"session,omitempty"`
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
	return true
}
