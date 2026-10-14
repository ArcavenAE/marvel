package service

import (
	"context"
	"time"
)

// State is where a managed service's child stands, in words every provider can
// use. It is not the provider's own detail: a provider keeps that behind its
// own accessor.
type State string

const (
	// StateUp: the child is running.
	StateUp State = "up"
	// StateDown: no child is running and none is waiting to be started again.
	StateDown State = "down"
	// StateBackoff: the child exited and a restart is waiting out its delay.
	StateBackoff State = "backoff"
)

// Status is what the daemon asks of any provider. It carries plain values
// only, so this package imports nothing of marvel's.
type Status struct {
	State    State
	Ready    bool
	PID      int
	Restarts int
	// ObservedAt is when the provider took this reading.
	ObservedAt time.Time
}

// Lifecycle is how the daemon starts, stops, restarts, reloads and reads one
// managed service. It is instantiated per cluster Services entry and
// implemented by that provider's driver (docs/design/services-shape-
// requirements.md, MS-8 (a)).
type Lifecycle interface {
	// Start brings the service up and returns once it is ready or has failed.
	Start(ctx context.Context) error
	// Stop ends the supervision. With keep set the child is left running for a
	// successor to adopt.
	Stop(keep bool)
	// Restart replaces the child; reason is recorded where the provider
	// reports restarts.
	Restart(reason string) error
	// Reload asks the running child to re-read its configuration.
	Reload() error
	// Status returns one reading of the service.
	Status() Status
}

// EnvFingerprinter is optional. A provider whose child reads a value from its
// environment once at start implements it, so the daemon can tell whether the
// running child holds the value it would hold if started now.
type EnvFingerprinter interface {
	// EnvFingerprint returns a non-reversible identifier for the value the
	// running child was started with under key, and false when the child was
	// started without one. Only fingerprints cross this interface.
	EnvFingerprint(key string) (fp string, ok bool)
}
