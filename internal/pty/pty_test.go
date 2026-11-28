package pty

import (
	"testing"

	"golang.org/x/sys/unix"
)

// A pty opens, and a size set on the master is the size the slave reports.
func TestOpenGivesAPairWhoseSizeFollowsResize(t *testing.T) {
	p, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		_ = p.Master.Close()
		_ = p.Slave.Close()
	})
	if err := p.Resize(30, 100); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	ws, err := unix.IoctlGetWinsize(int(p.Slave.Fd()), unix.TIOCGWINSZ) //nolint:gosec // a file descriptor number fits an int
	if err != nil {
		t.Fatalf("read slave size: %v", err)
	}
	if ws.Row != 30 || ws.Col != 100 {
		t.Errorf("slave size = %d rows %d cols, want 30 rows 100 cols", ws.Row, ws.Col)
	}
}

// Bytes written to the master arrive at the slave: the pair is one terminal.
func TestOpenPairCarriesBytesFromMasterToSlave(t *testing.T) {
	p, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		_ = p.Master.Close()
		_ = p.Slave.Close()
	})
	if _, err := p.Master.Write([]byte("ab\n")); err != nil {
		t.Fatalf("write master: %v", err)
	}
	buf := make([]byte, 16)
	n, err := p.Slave.Read(buf)
	if err != nil {
		t.Fatalf("read slave: %v", err)
	}
	if got := string(buf[:n]); got != "ab\n" {
		t.Errorf("slave read %q, want %q", got, "ab\n")
	}
}
