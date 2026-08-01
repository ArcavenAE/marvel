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
}

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
	ContextPercent  float64      `toml:"-"`
	LastHeartbeat   time.Time    `toml:"-"`
	HealthState     HealthState  `toml:"-"`
	FailureCount    int          `toml:"-"`
	RestartCount    int          `toml:"-"`
	LastHealthCheck time.Time    `toml:"-"`
	CreatedAt       time.Time    `toml:"-"`
	SessionMetrics  `toml:"-"`
}

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
}

// Team declares desired state: a cohesive unit of agents with heterogeneous roles.
type Team struct {
	Name       string     `toml:"name"`
	Workspace  string     `toml:"workspace"`
	Roles      []Role     `toml:"role"`
	Generation int64      `toml:"-"`
	Shift      ShiftState `toml:"-"`
	CreatedAt  time.Time
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
