package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestClusterScheduleHistoryMax: the cluster's ceiling on a scheduled
// role's history is read from schedule_history_max, and unset reads as 0
// (the daemon's default).
func TestClusterScheduleHistoryMax(t *testing.T) {
	t.Parallel()
	var cl Cluster
	if err := yaml.Unmarshal([]byte("name: local\nsocket: /tmp/m.sock\nschedule_history_max: 120\n"), &cl); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cl.ScheduleHistoryMax != 120 {
		t.Fatalf("ScheduleHistoryMax = %d, want 120", cl.ScheduleHistoryMax)
	}
	var unset Cluster
	if err := yaml.Unmarshal([]byte("name: local\n"), &unset); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if unset.ScheduleHistoryMax != 0 {
		t.Fatalf("unset ScheduleHistoryMax = %d, want 0", unset.ScheduleHistoryMax)
	}
}
