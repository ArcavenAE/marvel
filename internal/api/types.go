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
)

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

// Runtime is the program to execute (container image equivalent).
type Runtime struct {
	Name    string   `toml:"name"`
	Command string   `toml:"command"`
	Args    []string `toml:"args,omitempty"`
	Script  string   `toml:"script,omitempty"`
}

// Session is the atomic unit: a tmux pane running one process (pod equivalent).
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
}

// Role declares desired state for one kind of agent within a team.
type Role struct {
	Name          string        `toml:"name"`
	Replicas      int           `toml:"replicas"`
	Runtime       Runtime       `toml:"runtime"`
	RestartPolicy RestartPolicy `toml:"restart_policy,omitempty"`
	Permissions   string        `toml:"permissions,omitempty"`
	HealthCheck   *HealthCheck  `toml:"-"`
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

// Key returns the namespaced key for a resource.
func (w *Workspace) Key() string { return w.Name }
func (s *Session) Key() string   { return fmt.Sprintf("%s/%s", s.Workspace, s.Name) }
func (t *Team) Key() string      { return fmt.Sprintf("%s/%s", t.Workspace, t.Name) }
func (e *Endpoint) Key() string  { return fmt.Sprintf("%s/%s", e.Workspace, e.Name) }
