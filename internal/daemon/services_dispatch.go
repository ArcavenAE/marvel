package daemon

import (
	"fmt"
	"sort"
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
var attachArms = map[string]attachArm{
	service.ProviderNATSServer: func(d *Daemon, cl *config.Cluster, svc *config.Service, layout paths.Layout) (service.Lifecycle, error) {
		return d.attachBus(cl, svc, layout)
	},
}

// classCap is how many entries of a class one cluster may declare, with the
// noun its refusal uses. A class not listed has no cap.
var classCap = map[string]struct {
	max  int
	noun string
}{
	service.ClassMessageBus: {max: 1, noun: "bus"},
}

// servicesReadingTTL is how long a services row holds before it reads stale.
// Display only and arbitrary: a few seconds past the bus health tick, so a
// poll between ticks reads fresh. No decision reads it.
const servicesReadingTTL = 30 * time.Second

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

// attachEntries attaches each entry through the arm its provider names, in
// declared order. A provider with no arm refuses the start: the registries
// validated it, so a registry addition without a daemon arm fails loudly. The
// message-bus class holds at one entry per cluster. A bus arm's Lifecycle is
// also d.lifecycle, the one value, never a second instance.
func (d *Daemon) attachEntries(cl *config.Cluster, entries []config.Service, layout paths.Layout, arms map[string]attachArm) error {
	seen := map[string]int{}
	for i := range entries {
		svc := &entries[i]
		arm, ok := arms[svc.Provider]
		if !ok {
			return fmt.Errorf("services: cluster %s: service %q: no daemon arm for provider %q", cl.Name, svc.Name, svc.Provider)
		}
		if c, capped := classCap[svc.Class]; capped && seen[svc.Class] >= c.max {
			return fmt.Errorf("services: cluster %s declares a second %s entry %q; one %s per cluster today", cl.Name, svc.Class, svc.Name, c.noun)
		}
		seen[svc.Class]++
		lc, err := arm(d, cl, svc, layout)
		if err != nil {
			return err
		}
		if d.services == nil {
			d.services = map[string]attachedService{}
		}
		d.services[svc.Name] = attachedService{svc: *svc, lc: lc}
		if svc.Class == service.ClassMessageBus && lc != nil {
			d.lifecycle = lc
		}
	}
	return nil
}

// servicesStatus reads every attached entry through its provider interface,
// in name order. An entry with no child reports its source and nothing
// measured; a field the provider has no value for is absent, never zero.
func (d *Daemon) servicesStatus(now time.Time) []ServiceStatus {
	names := make([]string, 0, len(d.services))
	for n := range d.services {
		names = append(names, n)
	}
	sort.Strings(names)
	rows := make([]ServiceStatus, 0, len(names))
	for _, n := range names {
		a := d.services[n]
		row := ServiceStatus{Name: n, Class: a.svc.Class, Provider: a.svc.Provider, Mode: a.svc.Mode}
		row.Reading.Source = "provider:" + a.svc.Provider
		row.Reading.ObservedAt = now
		if a.lc != nil {
			st := a.lc.Status()
			if !st.ObservedAt.IsZero() {
				row.Reading.ObservedAt = st.ObservedAt.UTC()
			}
			v := ServiceReading{State: string(st.State), Ready: &st.Ready, Restarts: &st.Restarts}
			if st.PID > 0 {
				v.PID = &st.PID
			}
			row.Reading.Value = v
		}
		row.Reading.ValidUntil = row.Reading.ObservedAt.Add(servicesReadingTTL)
		rows = append(rows, row)
	}
	return rows
}
