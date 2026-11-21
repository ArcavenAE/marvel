//go:build darwin || linux

package procargs

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// startReader starts this test binary as the helper process, with exactly the
// given environment plus the helper switch, and the extra arguments after `--`,
// with a stdin it holds open. The pid is the helper's own.
func startReader(t *testing.T, env []string, extraArgs ...string) int {
	t.Helper()
	args := append([]string{"-test.run=^TestHoldUntilStdinCloses$", "--"}, extraArgs...)
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(append([]string(nil), env...), helperEnv+"=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Wait() })
	return cmd.Process.Pid
}

// readMarkerWhenReady reads the child's marker until the child has exec'd the
// shell (right after fork its environment is the parent's for an instant).
func readMarkerWhenReady(t *testing.T, pid int, ready func(Marker) bool) Marker {
	t.Helper()
	var m Marker
	var err error
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if m, err = ReadMarker(pid); err == nil && ready(m) {
			return m
		}
	}
	t.Fatalf("ReadMarker(%d) = %+v, %v; never reached the expected marker", pid, m, err)
	return m
}

// ReadMarker reads a live process's marker from the kernel: the MARVEL_SESSION
// of its environment, and not the one in its argument list.
func TestReadMarkerReadsTheEnvironmentNotArgv(t *testing.T) {
	pid := startReader(t, []string{"PATH=/usr/bin:/bin", "SECRET_X=supersecret", "MARVEL_SESSION=from-env"}, "MARVEL_SESSION=from-argv")
	m := readMarkerWhenReady(t, pid, func(m Marker) bool { return m.Session != "" })
	if m.Session != "from-env" {
		t.Errorf("Session = %q, want from-env (the environment value, not the argv one)", m.Session)
	}
	if m.MarvelPath {
		t.Error("MarvelPath is true with no marvel path in the environment")
	}
}

// A process with no marker reads as the zero marker, and a marvel test home in
// MARVEL_HOME is the path marker.
func TestReadMarkerNegativeAndPathMarker(t *testing.T) {
	plain := startReader(t, []string{"PATH=/usr/bin:/bin"}, "MARVEL_SESSION=only-in-argv")
	pathed := startReader(t, []string{"PATH=/usr/bin:/bin", "MARVEL_HOME=/tmp/marvel-test-home-77/.marvel"})

	// The pathed child proves the reader works on this host before the negative
	// is believed.
	if m := readMarkerWhenReady(t, pathed, func(m Marker) bool { return m.MarvelPath }); m.Session != "" {
		t.Errorf("pathed child Session = %q, want none", m.Session)
	}
	m, err := ReadMarker(plain)
	if err != nil {
		t.Fatalf("ReadMarker(plain): %v", err)
	}
	if m.Marvel() {
		t.Errorf("a process with no marvel variable read as %+v, want the zero marker", m)
	}
}
