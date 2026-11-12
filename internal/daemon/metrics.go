package daemon

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/procstat"
)

// RunMetrics samples per-session CPU and memory every interval until ctx
// is cancelled. Exported so tests can drive the loop without starting a
// listener.
func (d *Daemon) RunMetrics(ctx context.Context, interval time.Duration) {
	sampler := procstat.NewSampler()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// A first pass right away so `marvel get sessions` has numbers
	// before the first tick. On platforms whose CPU reading is a delta
	// between passes it also seeds the baseline.
	var held metricsHold
	d.metricsPass(sampler, &held)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.metricsPass(sampler, &held)
		}
	}
}

// metricsHold remembers that the last pass was skipped for a suspend, so the
// pass after the hold lifts can reseed the sampler instead of publishing.
type metricsHold struct{ skipped bool }

// metricsPass is one tick of RunMetrics. While the cluster is suspended it
// writes nothing: the processes are stopped, so every sample would read 0% CPU.
// The first pass after the hold lifts only reads the sampler, to move its
// baseline, and publishes nothing, because its CPU delta would span the whole
// pause; the next pass publishes.
func (d *Daemon) metricsPass(sampler *procstat.Sampler, h *metricsHold) {
	d.vitalsTick()
	if d.clusterSuspended() {
		h.skipped = true
		return
	}
	if h.skipped {
		h.skipped = false
		if pids := d.sampledPids(); len(pids) > 0 {
			_, _ = sampler.Sample(pids)
		}
		return
	}
	d.SampleMetricsOnce(sampler)
}

// sampledPids is the pane pid of every session the sampler reads.
func (d *Daemon) sampledPids() []int {
	var pids []int
	for _, s := range d.store.ListSessions() {
		if s.PID > 0 && s.State.CountsAsAlive() {
			pids = append(pids, s.PID)
		}
	}
	return pids
}

// SampleMetricsOnce runs one sampler pass and writes the results into the
// store. Sessions with no pid are skipped, leaving MetricsAt zero so the
// CLI renders absence rather than zero usage.
func (d *Daemon) SampleMetricsOnce(sampler *procstat.Sampler) {
	sessions := d.store.ListSessions()

	// The accountant is event-driven and needs no timer of its own, but a
	// missed Forget would leak per-session state for the daemon's life.
	// One map intersection on the pass that already has the session list
	// is cheaper than a second ticker.
	live := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		live[s.Key()] = true
	}
	d.usage.Sweep(live)

	pids := make([]int, 0, len(sessions))
	for _, s := range sessions {
		if s.PID > 0 && s.State.CountsAsAlive() {
			pids = append(pids, s.PID)
		}
	}
	if len(pids) == 0 {
		return
	}

	samples, err := sampler.Sample(pids)
	if err != nil {
		d.metricsWarn.Do(func() {
			if errors.Is(err, procstat.ErrUnsupported) {
				log.Printf("process metrics unavailable on this platform; CPU and RSS will read as -")
				return
			}
			log.Printf("process metrics: %v (further failures suppressed)", err)
		})
		return
	}

	for _, s := range sessions {
		sample, ok := samples[s.PID]
		if !ok {
			continue
		}
		d.store.UpdateSessionMetrics(s.Key(), metricsFrom(sample))
	}
}

// metricsFrom is the stored reading for one process-subtree sample.
func metricsFrom(sample procstat.Sample) api.SessionMetrics {
	return api.SessionMetrics{
		CPUPercent:   sample.CPUPercent,
		Millicpu:     api.MillicpuFromPercent(sample.CPUPercent),
		RSSBytes:     sample.RSSBytes,
		IOReadBytes:  sample.IOReadBytes,
		IOWriteBytes: sample.IOWriteBytes,
		IOAvailable:  sample.IOAvailable,
	}
}
