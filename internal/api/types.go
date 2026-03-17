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
	Name           string       `toml:"name"`
	Workspace      string       `toml:"workspace"`
	Team           string       `toml:"team"`
	Runtime        Runtime      `toml:"runtime"`
	State          SessionState `toml:"-"`
	PaneID         string       `toml:"-"`
	PID            int          `toml:"-"`
	ContextPercent float64      `toml:"-"`
	LastHeartbeat  time.Time    `toml:"-"`
	CreatedAt      time.Time    `toml:"-"`
}

// Team declares desired state: N sessions of a runtime (deployment equivalent).
type Team struct {
	Name      string    `toml:"name"`
	Workspace string    `toml:"workspace"`
	Replicas  int       `toml:"replicas"`
	Runtime   Runtime   `toml:"runtime"`
	Role      string    `toml:"role,omitempty"`
	CreatedAt time.Time
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
func (w *Workspace) Key() string  { return w.Name }
func (s *Session) Key() string    { return fmt.Sprintf("%s/%s", s.Workspace, s.Name) }
func (t *Team) Key() string       { return fmt.Sprintf("%s/%s", t.Workspace, t.Name) }
func (e *Endpoint) Key() string   { return fmt.Sprintf("%s/%s", e.Workspace, e.Name) }
