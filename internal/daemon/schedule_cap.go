package daemon

import (
	"errors"
	"log"

	"github.com/arcavenae/marvel/internal/config"
)

// scheduleHistoryMaxFrom reads the cluster's ceiling on schedule.history
// (schedule_history_max). No cluster entry, or a value below 1, returns
// 0, which apply reads as api.DefaultScheduleHistoryMax.
func scheduleHistoryMaxFrom(cl *config.Cluster) int {
	if cl == nil || cl.ScheduleHistoryMax < 1 {
		return 0
	}
	return cl.ScheduleHistoryMax
}

// configLoadUsable reports whether config.Load's error still leaves a
// config worth reading: a bad cluster name, bus or service list comes back
// beside the parsed config, and each caller proceeds per cluster. Any other
// error means no config was read.
func configLoadUsable(err error) bool {
	return err == nil || errors.Is(err, config.ErrInvalidClusterName) ||
		errors.Is(err, config.ErrInvalidBus) || errors.Is(err, config.ErrInvalidService)
}

// loadScheduleHistoryMax sets the daemon's history ceiling from the
// cluster entry whose socket is ours, found the way attachServices finds
// it and under the same tolerance for a config that loads with a
// validation error (configLoadUsable). A config that cannot be read at
// all leaves the default.
func (d *Daemon) loadScheduleHistoryMax(socketPath string) {
	cfg, err := config.Load()
	if !configLoadUsable(err) || cfg == nil {
		return
	}
	cl := cfg.ClusterForSocket(socketPath)
	if cl != nil && cl.ScheduleHistoryMax < 0 {
		log.Printf("schedule: cluster %s: schedule_history_max %d is below 1; using the default", cl.Name, cl.ScheduleHistoryMax)
	}
	d.scheduleHistoryMax = scheduleHistoryMaxFrom(cl)
}
