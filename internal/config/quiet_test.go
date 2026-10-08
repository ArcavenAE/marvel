package config

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// One knob moves the watchdog and the activity window: the operator's
// watchdog.window is the cluster's quiet window, and unset it is the default.
func TestQuietWindowFollowsWatchdogWindowConfig(t *testing.T) {
	set := &Config{Watchdog: Watchdog{Window: "3m"}}
	cluster, err := set.WatchdogWindow()
	if err != nil {
		t.Fatal(err)
	}
	if got := api.QuietWindow(&api.Role{}, cluster); got != 3*time.Minute {
		t.Errorf("with watchdog.window 3m the quiet window is %v, want 3m", got)
	}

	unset := &Config{}
	cluster, err = unset.WatchdogWindow()
	if err != nil {
		t.Fatal(err)
	}
	if got := api.QuietWindow(&api.Role{}, cluster); got != api.DefaultQuietWindow {
		t.Errorf("with watchdog.window unset the quiet window is %v, want %v", got, api.DefaultQuietWindow)
	}
}
