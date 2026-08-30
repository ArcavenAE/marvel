// Package api defines marvel's resource types — the k8s-like primitives
// for agent orchestration.
package api

import (
	"fmt"
	"time"
)

// SessionState represents the lifecycle state of a session.
type SessionState string

const (
	SessionPending   SessionState = "pending"
	SessionRunning   SessionState = "running"
	SessionSucceeded SessionState = "succeeded"
	SessionFailed    SessionState = "failed"
	// SessionCrashLoopBackOff is the state the reconciler assigns to a
	// role whose replicas keep failing faster than they start, while
	// backoff is in effect. Borrowed from Kubernetes' vocabulary.
	// The session stays in this state until backoff elapses and
	// another restart is attempted, or MaxRestarts is reached and the
	// state transitions to Failed.
	SessionCrashLoopBackOff SessionState = "crashloop-backoff"
	// SessionCrashed is the transition state set by ReapDead when the
	// underlying tmux pane vanished (clean exit, manual kill, runtime
	// binary crashed). The session is kept in the store — with PaneID
	// cleared — so operators see the event via `marvel get sessions`
	// during the backoff window. The reconciler does not count Crashed
	// sessions toward replica totals, and clears any stale Crashed
	// sessions for a role at the moment it spawns a replacement. See
	// ArcavenAE/marvel#10, aae-orc-8ci.
	SessionCrashed SessionState = "crashed"
)

// CountsAsAlive reports whether a session in this state should count
// toward a role's replica total. Pending and Running are obviously alive;
// CrashLoopBackOff sessions still have a live pane (the reconciler is
// deliberately not restarting them), so they are counted too. Succeeded,
// Failed, and Crashed sessions are terminal markers kept for visibility
// and do NOT count.
func (s SessionState) CountsAsAlive() bool {
	switch s {
	case SessionPending, SessionRunning, SessionCrashLoopBackOff:
		return true
	}
	return false
}

// HealthState represents the health of a session.
type HealthState string

const (
	HealthUnknown   HealthState = "unknown"
	HealthHealthy   HealthState = "healthy"
	HealthUnhealthy HealthState = "unhealthy"
)

// RestartPolicy controls what happens when a session becomes unhealthy.
type RestartPolicy string

const (
	RestartAlways    RestartPolicy = "always"
	RestartOnFailure RestartPolicy = "on-failure"
	RestartNever     RestartPolicy = "never"
)

// HealthCheckType identifies the kind of health check.
type HealthCheckType string

const (
	HealthCheckHeartbeat    HealthCheckType = "heartbeat"
	HealthCheckProcessAlive HealthCheckType = "process-alive"
)

// HealthCheck configures health checking for a role's sessions.
type HealthCheck struct {
	Type             HealthCheckType
	Timeout          time.Duration
	FailureThreshold int
}

// Workspace is an isolation boundary (namespace equivalent).
type Workspace struct {
	Name      string    `toml:"name"`
	CreatedAt time.Time `toml:"-"`
}

// RuntimeMode selects how a harness is launched within its pane.
type RuntimeMode string

const (
	// RuntimeModeInteractive attaches the harness to the pane's tty. The
	// zero value, so every manifest written before headless mode existed
	// keeps its behavior.
	RuntimeModeInteractive RuntimeMode = ""
	// RuntimeModeHeadless runs one non-interactive request whose
	// structured output marvel parses. Adapters that can redirect that
	// output declare it via runtime.StreamCapable; the session manager
	// then observes the session instead of only supervising the pane.
	RuntimeModeHeadless RuntimeMode = "headless"
)

// Runtime is the program to execute (container image equivalent).
type Runtime struct {
	Name    string   `toml:"name"`
	Command string   `toml:"command"`
	Args    []string `toml:"args,omitempty"`
	Script  string   `toml:"script,omitempty"`
	// Mode is interactive (default) or headless.
	Mode RuntimeMode `toml:"mode,omitempty"`
	// Prompt is the request a headless launch carries. Required in
	// headless mode: a harness given no prompt reads stdin, and stdin in
	// a detached pane is a tty nobody types into.
	Prompt string `toml:"prompt,omitempty"`
	// ContextWindow overrides the model-to-limit table for this runtime,
	// in tokens. The escape hatch for a model marvel does not know: a
	// shipped table tracks the vendor's release cadence, not marvel's. A
	// window the harness declares itself outranks this, because the
	// harness's own belief is what enforces compaction.
	ContextWindow int `toml:"context_window,omitempty"`
	// ContextFeed opts an interactive session into a cooperative context
	// pressure feed. The only value today is "statusline": the projection
	// layer injects statusLine/subagentStatusLine hooks pointing at
	// `marvel ctx-forward`, which forwards the harness's own context
	// figures to the heartbeat RPC. Headless sessions do not need this —
	// their stream already feeds the usage accountant. See finding-011.
	ContextFeed string `toml:"context_feed,omitempty"`
}

// ContextFeedStatusline is the only ContextFeed value marvel understands
// today: statusline-hook forwarding for interactive claude sessions.
const ContextFeedStatusline = "statusline"

// Session is the atomic unit: a tmux pane running one process (pod equivalent).
//
// PID is tmux's pane_pid: the shell tmux started, not the agent binary
// it exec'd. Resource readings are therefore a rollup over the pid's
// subtree, not a read of the pid itself. See internal/procstat.
type Session struct {
	Name            string       `toml:"name"`
	Workspace       string       `toml:"workspace"`
	Team            string       `toml:"team"`
	Role            string       `toml:"role"`
	Generation      int64        `toml:"-"`
	Runtime         Runtime      `toml:"runtime"`
	State           SessionState `toml:"-"`
	PaneID          string       `toml:"-"`
	PID             int          `toml:"-"`
	LastHeartbeat   time.Time    `toml:"-"`
	HealthState     HealthState  `toml:"-"`
	FailureCount    int          `toml:"-"`
	RestartCount    int          `toml:"-"`
	LastHealthCheck time.Time    `toml:"-"`
	CreatedAt       time.Time    `toml:"-"`
	// Reason is a projection-only annotation: empty on every real session
	// row, filled by the read-path join (team.Controller.ProjectHeldRoleRows)
	// on the synthetic rows it invents for a role held down with no live
	// session — e.g. "restart #N, backoff until T". json omitempty keeps it
	// out of every persisted and real-row payload (so no bolt schema bump),
	// and toml:"-" keeps it out of manifests. See aae-orc-prhx.
	Reason string `json:"reason,omitempty" toml:"-"`
	// HeartbeatToken is the secret marvel mints at spawn and injects into
	// the session's process environment. It binds a heartbeat to the
	// session that claims it: the RPC takes a session key off the wire,
	// and without this any process reaching the socket could stamp any
	// session's liveness and context pressure.
	//
	// `json:"-"` is load-bearing twice over. The store's records go to
	// bbolt through encoding/json, and ListSessions goes to every RPC
	// client the same way, so a serialized plaintext token would be
	// readable by exactly the sibling agents it is meant to separate. The
	// plaintext therefore lives in this process's memory and in the
	// spawned agent's environment, nowhere else. HeartbeatTokenHash is
	// what persists.
	HeartbeatToken string `json:"-" toml:"-"`
	// HeartbeatTokenHash is the SHA-256 of HeartbeatToken, hex-encoded.
	// It persists and it travels on the wire, which is safe: a 256-bit
	// random token is not recoverable from its digest, and verification
	// only ever needs to recompute it.
	//
	// Persisting the hash is what lets an adopted session keep beating
	// across a daemon restart. The agent still holds the plaintext in its
	// environment; the rehydrated record still holds the digest to check
	// it against.
	//
	// Empty means the record predates this field, which is the one case
	// AuthenticateHeartbeat admits unbound. See its comment.
	HeartbeatTokenHash string `toml:"-"`
	SessionMetrics     `toml:"-"`
	SessionContext     `toml:"-"`
}

// SessionContext is one context-window reading for a session.
//
// ContextAt separates "measured" from "never measured", exactly as
// SessionMetrics.MetricsAt does. ContextLimit == 0 further separates
// "tokens measured, window unknown" from both, so callers render three
// states rather than a convincing percentage against a guessed window.
// See orc finding-055 for the recorded cost of a wrong denominator.
//
// The reading is raw occupancy against the model's window, which is not
// the figure a harness displays to its own user (see internal/usage).
type SessionContext struct {
	// ContextSource names which producer wrote this reading. Two write
	// the same field set from different places (the usage accountant
	// parsing a stream, and a cooperative agent reporting a percentage
	// it computed itself), and before this field existed three sites
	// inferred the producer from which fields happened to be populated:
	// the CLI renderer, the bolt rehydrate path, and the store's own
	// reasoning about what to keep.
	//
	// Shape is not provenance. An accountant reading that could not
	// resolve a window is shaped like nothing at all, so a later
	// cooperative reading on the same session was rendered absent. See
	// aae-orc-ibu9. Declared, never inferred, exactly as
	// ContextLimitSource already is for the denominator.
	ContextSource  ContextSourceKind
	ContextPercent float64
	ContextTokens  int
	ContextLimit   int
	// ContextLimitSource names the rung of the resolution ladder that
	// produced ContextLimit, so a consumer can tell a measured window
	// from a table guess. Values are usage.LimitSource.
	ContextLimitSource string
	ContextModel       string
	ContextRequests    int
	ContextCompactions int
	// ContextPeak is the high-water ContextPercent, valid when
	// ContextLimit > 0.
	ContextPeak float64
	ContextAt   time.Time
}

// ContextSourceKind identifies a CTX% producer.
type ContextSourceKind string

const (
	// ContextSourceNone is the zero value: no reading has landed.
	ContextSourceNone ContextSourceKind = ""
	// ContextSourceAccountant is the usage accountant reading a parsed
	// harness stream. Carries a token count and a request count, and may
	// carry no window at all when the model is unresolved.
	ContextSourceAccountant ContextSourceKind = "accountant"
	// ContextSourceHeartbeat is a cooperative agent reporting a
	// percentage it computed itself over the heartbeat RPC. Carries no
	// token count, no window, and no request count: the percentage IS
	// the whole reading.
	ContextSourceHeartbeat ContextSourceKind = "heartbeat"
)

// SessionMetrics is one process-sampler reading for a session, rolled up
// over the pid subtree.
//
// MetricsAt separates "sampled, idle" from "never sampled": a session
// with no PID, or one running where marvel has no process-table reader,
// keeps MetricsAt zero and callers render absence instead of a
// convincing 0.0.
type SessionMetrics struct {
	CPUPercent float64
	RSSBytes   int64
	// IOReadBytes and IOWriteBytes are cumulative block-layer bytes over
	// the subtree. IOAvailable is false where the platform exposes no
	// per-process counter (darwin today).
	IOReadBytes  int64
	IOWriteBytes int64
	IOAvailable  bool
	MetricsAt    time.Time
}

// Role declares desired state for one kind of agent within a team.
// Name is the job function (reviewer, supervisor, probe-runner).
// Persona and Identity are the costume and lens per finding-019.
type Role struct {
	Name          string        `toml:"name"`
	Replicas      int           `toml:"replicas"`
	Runtime       Runtime       `toml:"runtime"`
	RestartPolicy RestartPolicy `toml:"restart_policy,omitempty"`
	Permissions   string        `toml:"permissions,omitempty"`
	// DangerousPermissions, when true, causes adapters that support it to
	// append --dangerously-skip-permissions (or equivalent) to the spawned
	// agent. Intended for autonomous marvel-managed teams where no
	// interactive approver exists. Per orc finding-023, the permission UI
	// is a cooperative contract; real enforcement belongs to curtain.
	// Combined with a curtain profile, this is the default sensible shape
	// for autonomous fleet agents.
	//
	// DangerousPermissions is orthogonal to Permissions and combines with
	// any permission mode. Permissions maps to Claude Code's
	// --permission-mode (one of acceptEdits, auto, bypassPermissions,
	// default, dontAsk, plan) and selects HOW the harness prompts within
	// its cooperative permission model. DangerousPermissions instead
	// appends --dangerously-skip-permissions, which removes that model.
	// An operator picks permissions: bypassPermissions to keep the
	// harness's permission machinery engaged (still auditable, still
	// hookable) while auto-allowing within it; they pick
	// dangerous_permissions: true to skip the machinery outright. The two
	// are validated independently — permission_mode against the canonical
	// mode set (see api.canonicalPermissionModes), dangerous_permissions
	// as a free boolean.
	DangerousPermissions bool   `toml:"dangerous_permissions,omitempty"`
	Persona              string `toml:"persona,omitempty"`  // character slug (e.g. "naomi-nagata")
	Identity             string `toml:"identity,omitempty"` // professional lens (e.g. "homicide detective")
	// Policy names the Policy this role's sessions are projected with —
	// the contract half of finding-024: a Claude Code settings fragment
	// marvel writes to a per-session file the harness reads. Empty means
	// no projection. Resolved within the role's workspace. The sandbox
	// half (a curtain profile) stays parked with aae-orc-10x.
	Policy      string       `toml:"policy,omitempty"`
	HealthCheck *HealthCheck `toml:"-"`
	// MaxRestarts caps the number of restarts for any single replica
	// slot in this role before the reconciler gives up and leaves the
	// session in SessionFailed. Zero means unlimited; negative values
	// are clamped to zero. See ArcavenAE/marvel#11.
	MaxRestarts int `toml:"max_restarts,omitempty"`
}

// ShiftPhase represents the current phase of a shift operation.
type ShiftPhase string

const (
	ShiftNone      ShiftPhase = ""
	ShiftLaunching ShiftPhase = "launching"
	ShiftDraining  ShiftPhase = "draining"
)

// ShiftState tracks an in-progress shift operation on a team.
type ShiftState struct {
	Phase         ShiftPhase
	OldGeneration int64
	RoleIndex     int      // index into Roles (shift order)
	Roles         []string // role names in shift order (supervisor last)
	StartedAt     time.Time
	// Drained counts, per role name, the old-generation sessions this shift
	// actually deleted while draining that role. It lets shiftDrain tell a
	// completed drain (Drained[role] > 0) from advancing through a role
	// whose old generation was already empty (Drained[role] == 0), which
	// len(oldGen)==0 alone cannot. Status, not spec — same treatment as the
	// rest of ShiftState (the whole field is toml:"-" on Team); it persists
	// to bolt with the shift and resets to nil when the shift clears. See
	// aae-orc-094e.
	Drained map[string]int
}

// Team declares desired state: a cohesive unit of agents with heterogeneous roles.
type Team struct {
	Name      string `toml:"name"`
	Workspace string `toml:"workspace"`
	Roles     []Role `toml:"role"`
	// Budget is the team's declared resource ceiling. The zero value
	// declares no gate, which is every manifest written before this field
	// existed. See budget.go and aae-orc-qiay.
	Budget     Budget     `toml:"budget,omitempty"`
	Generation int64      `toml:"-"`
	Shift      ShiftState `toml:"-"`
	// Admission is the standing admission condition the reconciler
	// recomputes each tick. Status, not spec — same treatment as Shift.
	Admission AdmissionState `toml:"-"`
	// ConvergencePosture is the team's runtime stance on COLD convergence
	// toward desired. Status, not spec (toml:"-", same as Shift/Admission):
	// an operator never declares it in a manifest; the daemon sets it at start
	// from what adoption found, and `marvel converge` flips it. Read it through
	// Posture(), which normalizes the zero value to the safe default.
	// See question-convergence-posture, aae-orc-rwiw / cxdf.
	ConvergencePosture ConvergencePosture `toml:"-"`
	CreatedAt          time.Time
}

// ConvergencePosture is a team's runtime stance on cold convergence toward its
// desired replica count (question-convergence-posture, aae-orc-rwiw). It is
// status, not spec: an operator never declares it in a manifest, the daemon
// sets it at start from what adoption found, and `marvel converge` flips it. It
// is JSON-persisted with the rest of the Team so `describe` can report it, but
// the daemon re-derives it from live presence on every start (see
// team.Controller.InitConvergencePosture), so a persisted value never
// authorizes a cold spawn on its own — the money-safety guarantee for
// aae-orc-cxdf, where a daemon start rehydrated stale desired state and spawned
// a fleet nobody asked for.
type ConvergencePosture string

const (
	// PostureHold withholds COLD convergence: a team with no live presence
	// does not spawn toward desired until an explicit converge. This is the
	// default — "hold at the start line" — and the zero value ("") normalizes
	// to it via Team.Posture, so a team record written before this field
	// existed reads as held.
	PostureHold ConvergencePosture = "hold"
	// PostureConverge spawns toward desired: the operator's go-line, and the
	// stance a team with surviving panes is given at start so its steady-state
	// maintenance (crash-loop restarts, replica top-ups) is never suppressed.
	PostureConverge ConvergencePosture = "converge"
)

// Posture returns the team's convergence posture, normalizing the zero value
// (an unset field — a team record written before the field existed, or one the
// daemon has not yet initialized) to the safe default PostureHold. Only an
// explicit PostureConverge reads as converge; anything else holds. This
// asymmetry is deliberate: a garbled or absent value must never authorize a
// cold spawn.
func (t *Team) Posture() ConvergencePosture {
	if t.ConvergencePosture == PostureConverge {
		return PostureConverge
	}
	return PostureHold
}

// Endpoint is a stable name for a session role (service equivalent).
type Endpoint struct {
	Name      string `toml:"name"`
	Workspace string `toml:"workspace"`
	Team      string `toml:"team"`
}

// Host represents the local machine (node equivalent).
type Host struct {
	Name   string
	Status string
}

// Policy is a named Claude Code settings fragment marvel projects into a
// per-session file (the ConfigMap equivalent in the finding-024 model).
// It is the contract half only: Settings is written verbatim as JSON to
// the file the harness reads (permissions.allow/deny, hooks, and the rest
// of the Claude Code settings surface). Marvel owns the truth and does not
// interpret the contents. The sandbox half (a curtain profile) is a
// separate resource parked with aae-orc-10x; tamper-proofing the projected
// file is aae-orc-wbqi. Scoped to a workspace and referenced by a Role's
// Policy field.
type Policy struct {
	Name      string `toml:"name"`
	Workspace string `toml:"-"`
	// Version is an operator-facing label (e.g. "1.2"). Marvel does not
	// resolve on it today — it rides along for observability and future
	// pinning.
	Version string `toml:"version,omitempty"`
	// Settings is the Claude Code settings fragment, written verbatim as
	// JSON. It is map[string]any because marvel is a pass-through for a
	// third party's schema (Claude Code's), not an owner of it — pinning a
	// Go struct here would couple marvel's releases to Claude Code's
	// settings schema for no gain.
	Settings  map[string]any `toml:"-"`
	CreatedAt time.Time      `toml:"-"`
}

// Key returns the namespaced key for a resource.
func (w *Workspace) Key() string { return w.Name }
func (s *Session) Key() string   { return fmt.Sprintf("%s/%s", s.Workspace, s.Name) }
func (t *Team) Key() string      { return fmt.Sprintf("%s/%s", t.Workspace, t.Name) }
func (e *Endpoint) Key() string  { return fmt.Sprintf("%s/%s", e.Workspace, e.Name) }
func (p *Policy) Key() string    { return fmt.Sprintf("%s/%s", p.Workspace, p.Name) }
