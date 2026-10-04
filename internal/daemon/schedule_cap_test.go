package daemon

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/config"
)

// TestApplyEnforcesTheClusterHistoryCap: apply refuses a scheduled role
// whose history exceeds the cluster's schedule_history_max, 50 when the
// cluster sets none, and accepts it once the cluster raises the cap.
func TestApplyEnforcesTheClusterHistoryCap(t *testing.T) {
	d := newHandlerDaemon(t)
	manifest := func(ws string) string {
		return scheduledManifest(ws, `          cron: "17 6 * * *"
          timezone: Etc/UTC
          history:
            succeeded: 3
            failed: 55`)
	}
	resp := applyManifest(t, d, manifest("capped"))
	if resp.Error == "" || !strings.Contains(resp.Error, "at most 50") {
		t.Fatalf("apply over the default cap: error %q, want a refusal naming 50", resp.Error)
	}
	if _, err := d.store.GetTeam("capped/timers-capped"); err == nil {
		t.Fatal("a refused apply stored the team")
	}

	d.scheduleHistoryMax = 60
	if resp := applyManifest(t, d, manifest("raised")); resp.Error != "" {
		t.Fatalf("apply under a raised cap: %s", resp.Error)
	}
}

// TestScheduleHistoryMaxFromCluster: the daemon takes the cap from its
// cluster entry; no entry, or a value below 1, leaves the default.
func TestScheduleHistoryMaxFromCluster(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cl   *config.Cluster
		want int
	}{
		{"no cluster entry", nil, 0},
		{"unset", &config.Cluster{Name: "local"}, 0},
		{"negative is ignored", &config.Cluster{Name: "local", ScheduleHistoryMax: -4}, 0},
		{"set", &config.Cluster{Name: "local", ScheduleHistoryMax: 120}, 120},
	}
	for _, tc := range tests {
		if got := scheduleHistoryMaxFrom(tc.cl); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}
