package daemon

import (
	"testing"
	"time"
)

// TestOptionsShiftTimeoutWired asserts that a ShiftTimeout set on Options
// reaches the team controller, and that leaving it zero keeps the
// controller on its built-in default (surfaced as ShiftTimeout==0, which
// the controller reads as "use defaultShiftTimeout"). See aae-orc-sape /
// ArcavenAE/marvel#88.
func TestOptionsShiftTimeoutWired(t *testing.T) {
	t.Run("explicit value is applied", func(t *testing.T) {
		d, err := NewWithOptions(Options{ShiftTimeout: 90 * time.Second})
		if err != nil {
			t.Fatalf("new daemon: %v", err)
		}
		if got := d.teamCtrl.ShiftTimeout; got != 90*time.Second {
			t.Errorf("controller ShiftTimeout = %s, want 90s", got)
		}
	})

	t.Run("unset leaves controller default (zero)", func(t *testing.T) {
		d, err := NewWithOptions(Options{})
		if err != nil {
			t.Fatalf("new daemon: %v", err)
		}
		if got := d.teamCtrl.ShiftTimeout; got != 0 {
			t.Errorf("controller ShiftTimeout = %s, want 0 (controller falls back to 10m default)", got)
		}
	})
}
