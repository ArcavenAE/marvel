package daemon

import (
	"fmt"

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

// attachedService is one attached entry.
type attachedService struct {
	svc config.Service
	lc  service.Lifecycle
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
