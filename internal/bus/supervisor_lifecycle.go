package bus

import (
	"context"
	"time"

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

// Start brings the broker up; see Supervisor.Start.
func (l *Lifecycle) Start(ctx context.Context) error { return l.s.Start(ctx) }

// Stop ends the supervision, leaving the child running when keep is set.
func (l *Lifecycle) Stop(keep bool) { l.s.Stop(keep) }

// Restart replaces the broker process; see Supervisor.Restart.
func (l *Lifecycle) Restart(reason string) error { return l.s.Restart(reason) }

// Reload asks the running broker to re-read its configuration.
func (l *Lifecycle) Reload() error { return l.s.Reload() }

// Status is one reading in plain values. A broker with no pid reads as down, one
// waiting out a restart delay as backoff, any other as up. The detail the
// status RPC serves stays behind BusStatus until the as-of cells land
// (MS-8 (h)).
func (l *Lifecycle) Status() service.Status {
	b := l.s.Status()
	state := service.StateUp
	switch {
	case b.BackoffUntil != "":
		state = service.StateBackoff
	case b.PID == 0:
		state = service.StateDown
	}
	return service.Status{State: state, Ready: b.Ready, PID: b.PID, Restarts: b.Restarts, ObservedAt: time.Now()}
}

// EnvFingerprint reports the fingerprint of the leaf seed the running broker
// was spawned with, under LeafSeedKey, and false when it booted without one or
// the key is another.
func (l *Lifecycle) EnvFingerprint(key string) (string, bool) {
	if key != LeafSeedKey || !l.s.LeafSeedInEnv() {
		return "", false
	}
	return l.s.LeafSeedFingerprint(), true
}

// BusStatus is the broker-specific status the RPC serves: the one bus-typed
// call the daemon keeps.
func (l *Lifecycle) BusStatus() Status { return l.s.Status() }
