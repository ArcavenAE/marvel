package tmux

import "testing"

// A caller that reports the socket without holding a driver (daemon.status on
// a daemon with none) must not need a nil check of its own: the daemon's
// driver inventory counts every bare use of the driver field.
func TestSocketOfANilDriverIsEmpty(t *testing.T) {
	var d *Driver
	if got := d.Socket(); got != "" {
		t.Errorf("Socket() on a nil driver = %q, want empty", got)
	}
}

func TestSocketOfADriverIsItsName(t *testing.T) {
	d := &Driver{socket: "marvel-abc12345"}
	if got := d.Socket(); got != "marvel-abc12345" {
		t.Errorf("Socket() = %q, want the driver's name", got)
	}
}
