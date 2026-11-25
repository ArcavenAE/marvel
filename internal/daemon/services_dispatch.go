package daemon

import (
	"time"

	"github.com/arcavenae/marvel/internal/asof"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/paths"
	"github.com/arcavenae/marvel/internal/service"
)

// attachArm attaches one Services entry of one provider. It returns the
// entry's Lifecycle, or nil for an adopted provider that has no child.
type attachArm func(d *Daemon, cl *config.Cluster, svc *config.Service, layout paths.Layout) (service.Lifecycle, error)

// attachArms is the provider map attachServices dispatches through.
var attachArms = map[string]attachArm{}

// servicesReadingTTL is how long a services row holds before it reads stale.
// Display only and arbitrary: a few seconds past the bus health tick, so a
// poll between ticks reads fresh. No decision reads it.
const servicesReadingTTL = 30 * time.Second

// The stub reads nothing yet; the green change removes this line.
var _ = servicesReadingTTL

// attachedService is one attached entry.
type attachedService struct {
	svc config.Service
	lc  service.Lifecycle
}

// ServiceReading is the value of a services row. A field the provider did
// not measure is absent, never zero.
type ServiceReading struct {
	State    string `json:"state,omitempty"`
	Ready    *bool  `json:"ready,omitempty"`
	PID      *int   `json:"pid,omitempty"`
	Restarts *int   `json:"restarts,omitempty"`
}

// ServiceStatus is one row of daemon.status services.
type ServiceStatus struct {
	Name     string                    `json:"name"`
	Class    string                    `json:"class"`
	Provider string                    `json:"provider"`
	Mode     string                    `json:"mode"`
	Reading  asof.Cell[ServiceReading] `json:"reading"`
}

func (d *Daemon) attachEntries(cl *config.Cluster, entries []config.Service, layout paths.Layout, arms map[string]attachArm) error {
	return nil
}

func (d *Daemon) servicesStatus(now time.Time) []ServiceStatus { return nil }
