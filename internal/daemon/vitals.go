package daemon

import (
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/hostvitals"
	"github.com/arcavenae/marvel/internal/procstat"
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

// handleHostVitals answers host.vitals from the sampler's memory: the latest
// reading, its time, each threshold with its state and each gap. It reads
// nothing from the host and writes nothing, and a daemon whose sampler is off
// or was never built answers the off snapshot, not an error.
func (d *Daemon) handleHostVitals() daemonrpc.Response {
	return respond(d.vitals.Snapshot())
}

// vitalsDue reports whether the next vitals tick takes a reading: the sampler
// is on and this is the first pass or every VitalsEvery-th after it.
func (d *Daemon) vitalsDue() bool { return false }

// vitalsTickWith is vitalsTick with the process table the metrics pass read,
// when it read one.
func (d *Daemon) vitalsTickWith(_ []procstat.Proc, _ bool) { d.vitalsTick() }
