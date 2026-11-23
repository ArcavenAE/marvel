package daemon

import (
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/hostvitals"
	"github.com/arcavenae/marvel/internal/procargs"
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

// hostVitalsOrphanExclude reads the cluster's host_vitals_orphan_exclude list.
// No cluster entry, or an unset key, is the empty list.
func hostVitalsOrphanExclude(cl *config.Cluster) []string { return nil }

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
	d.readTable = procstat.ReadTable
	d.readMarker = procargs.ReadMarker
	d.wireOrphanSources()
}

// wireOrphanSources gives the sampler the seat roots and the marker reader the
// orphan vital needs (docs/design/host-vitals.md section 2.1): the pane pid of
// every live seat, and a reader that returns only a process's marvel marker.
func (d *Daemon) wireOrphanSources() {
	d.vitals.SetOrphanSources(hostvitals.OrphanSources{
		Roots: func() []int {
			if d.store == nil {
				return nil
			}
			return d.sampledPids()
		},
		Marker: d.readMarker,
		// The arguments and working directory of an unattributed process, read
		// once when it first crosses the orphan CPU line.
		Detail: hostDetailReaders().read,
		// The seat's own key, from the store: the one name an orphan carries
		// that the daemon vouches for. It answers for a live session only and
		// "" for one that is dead or deleted; the sampler asks while the seat's
		// pane is a root in the table and keeps the answer, so a seat that dies
		// later does not lose its orphans' key.
		RootSession: func(pid int) string {
			if d.store == nil {
				return ""
			}
			for _, s := range d.store.ListSessions() {
				if s.PID == pid && s.State.CountsAsAlive() {
					return s.Key()
				}
			}
			return ""
		},
	})
}

// vitalsDue reports whether the next vitals tick takes a reading: the sampler
// is on and this is the first pass or every VitalsEvery-th after it.
func (d *Daemon) vitalsDue() bool {
	return d.vitals.Enabled() && d.vitalsPass%VitalsEvery == 0
}

// vitalsTick is vitalsTickWith with no process table in hand.
func (d *Daemon) vitalsTick() { d.vitalsTickWith(nil, false) }

// vitalsTickWith is called once per metrics pass and takes a reading on the
// first pass and on every VitalsEvery-th one after it. table is the wide
// process table the metrics pass read, when it read one (a seat had a pid):
// the vitals reuse it and make no read of their own. With none in hand, the
// vitals pass reads one itself, once, which is the design's one ps a minute on
// a host with no seat (docs/design/host-vitals.md section 2.2); a daemon with
// no reader, or a read that fails, leaves the process vitals a gap. It reads
// and reports; no reading is passed to anything that spawns, signals or
// refuses.
func (d *Daemon) vitalsTickWith(table []procstat.Proc, ok bool) {
	if !d.vitals.Enabled() {
		return
	}
	if d.vitalsPass%VitalsEvery == 0 {
		if !ok && d.readTable != nil {
			if t, err := d.readTable(); err == nil {
				table, ok = t, true
			}
		}
		d.vitals.SampleProcs(vitalsProcs(table), ok)
	}
	d.vitalsPass++
}

// vitalsProcs converts the process table to the sampler's own type.
func vitalsProcs(table []procstat.Proc) []hostvitals.Proc {
	out := make([]hostvitals.Proc, 0, len(table))
	for _, p := range table {
		out = append(out, hostvitals.Proc{
			PID: p.PID, PPID: p.PPID, RSSBytes: uint64(max(p.RSSBytes, 0)),
			ElapsedSeconds: p.Elapsed.Seconds(), CPUSeconds: p.CPUTime.Seconds(), Name: p.Name,
		})
	}
	return out
}

// handleHostVitals answers host.vitals from the sampler's memory: the latest
// reading, its time, each threshold with its state and each gap. It reads
// nothing from the host and writes nothing, and a daemon whose sampler is off
// or was never built answers the off snapshot, not an error.
func (d *Daemon) handleHostVitals() daemonrpc.Response {
	return respond(d.vitals.Snapshot())
}
