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
	d.SampleMetricsOnce(sampler)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.SampleMetricsOnce(sampler)
		}
	}
}

// SampleMetricsOnce runs one sampler pass and writes the results into the
// store. Sessions with no pid are skipped, leaving MetricsAt zero so the
// CLI renders absence rather than zero usage.
func (d *Daemon) SampleMetricsOnce(sampler *procstat.Sampler) {
	sessions := d.store.ListSessions()
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
		d.store.UpdateSessionMetrics(s.Key(), api.SessionMetrics{
			CPUPercent:   sample.CPUPercent,
			RSSBytes:     sample.RSSBytes,
			IOReadBytes:  sample.IOReadBytes,
			IOWriteBytes: sample.IOWriteBytes,
			IOAvailable:  sample.IOAvailable,
		})
	}
}
