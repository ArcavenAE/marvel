package daemon

import (
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/hostvitals"
)

// VitalsEvery is how many metrics passes make one host vitals reading: every
// twelfth, so once a minute at MetricsInterval (docs/design/host-vitals.md
// section 2.2). The sampler adds no loop of its own.
const VitalsEvery = 12

// hostVitalsEnabled reads the cluster's host_vitals switch. No cluster entry,
// or an unset switch, is off.
func hostVitalsEnabled(cl *config.Cluster) bool {
	return cl != nil && cl.HostVitals
}

// loadHostVitals builds the daemon's host vitals sampler from the cluster
// entry whose socket is ours, found the way loadScheduleHistoryMax finds it
// and under the same tolerance for a config that loads with a validation
// error. A config that cannot be read at all leaves it off.
func (d *Daemon) loadHostVitals(socketPath string) {
	on := false
	if cfg, err := config.Load(); configLoadUsable(err) && cfg != nil {
		on = hostVitalsEnabled(cfg.ClusterForSocket(socketPath))
	}
	d.vitals = hostvitals.New(on, hostvitals.Read, d.events)
}

// vitalsTick is called once per metrics pass and takes a reading on the first
// pass and on every VitalsEvery-th one after it. It reads and reports; no
// reading is passed to anything that spawns, signals or refuses.
func (d *Daemon) vitalsTick() {
	if !d.vitals.Enabled() {
		return
	}
	if d.vitalsPass%VitalsEvery == 0 {
		d.vitals.Sample()
	}
	d.vitalsPass++
}
