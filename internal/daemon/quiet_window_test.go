package daemon

import (
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// The watchdog's default window is the cluster's default quiet window, not a
// second ten minutes that could drift from it.
func TestWatchdogWindowReadsDefaultQuietWindow(t *testing.T) {
	if DefaultWatchdogWindow != api.DefaultQuietWindow {
		t.Errorf("DefaultWatchdogWindow = %v, want api.DefaultQuietWindow %v", DefaultWatchdogWindow, api.DefaultQuietWindow)
	}
	if w := newWatchdog(nil, nil, nil, 0); w.window != api.DefaultQuietWindow {
		t.Errorf("a watchdog with no window uses %v, want %v", w.window, api.DefaultQuietWindow)
	}
}
