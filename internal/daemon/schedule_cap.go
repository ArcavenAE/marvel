package daemon

import (
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

// loadScheduleHistoryMax sets the daemon's history ceiling from the
// cluster entry whose socket is ours, the same lookup attachServices
// makes. An unreadable config leaves the default.
func (d *Daemon) loadScheduleHistoryMax(socketPath string) {
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return
	}
	cl := cfg.ClusterForSocket(socketPath)
	if cl != nil && cl.ScheduleHistoryMax < 0 {
		log.Printf("schedule: cluster %s: schedule_history_max %d is below 1; using the default", cl.Name, cl.ScheduleHistoryMax)
	}
	d.scheduleHistoryMax = scheduleHistoryMaxFrom(cl)
}
