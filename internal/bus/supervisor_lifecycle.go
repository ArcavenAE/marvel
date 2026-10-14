package bus

import (
	"context"

	"github.com/arcavenae/marvel/internal/service"
)

// LeafSeedKey names the leaf seed to EnvFingerprint: the credential the
// daemon stores it under.
const LeafSeedKey = "bus/leaf"

// Lifecycle is the daemon's handle on a Supervisor: it implements
// service.Lifecycle and service.EnvFingerprinter over it.
type Lifecycle struct{ s *Supervisor }

// NewLifecycle wraps s.
func NewLifecycle(s *Supervisor) *Lifecycle { return &Lifecycle{s: s} }

var (
	_ service.Lifecycle        = (*Lifecycle)(nil)
	_ service.EnvFingerprinter = (*Lifecycle)(nil)
)

// Start is a stub in the red commit.
func (l *Lifecycle) Start(context.Context) error { return nil }

// Stop is a stub in the red commit.
func (l *Lifecycle) Stop(bool) {}

// Restart is a stub in the red commit.
func (l *Lifecycle) Restart(string) error { return nil }

// Reload is a stub in the red commit.
func (l *Lifecycle) Reload() error { return nil }

// Status is a stub in the red commit.
func (l *Lifecycle) Status() service.Status { return service.Status{} }

// EnvFingerprint is a stub in the red commit.
func (l *Lifecycle) EnvFingerprint(string) (string, bool) { return "", false }

// BusStatus is a stub in the red commit.
func (l *Lifecycle) BusStatus() Status { return Status{} }
