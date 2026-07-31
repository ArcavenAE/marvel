//go:build linux

package procstat

import (
	"os"
	"path/filepath"
	"testing"
)

// readProcesses is exercised against a fixture tree rather than the live
// /proc so the expected values are fixed. The live path is covered by
// TestSampleSelf.
func TestReadProcessesFixtureTree(t *testing.T) {
	root := t.TempDir()
	write := func(pid string, files map[string]string) {
		dir := filepath.Join(root, pid)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for f, content := range files {
			if err := os.WriteFile(filepath.Join(dir, f), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}

	// ppid 1, utime+stime 473 ticks, rss 12800 pages.
	write("100", map[string]string{"stat": procStatFixture})
	write("200", map[string]string{
		"stat": "200 (agent) S 100 200 200 0 0 0 0 0 0 0 100 100 0 0 20 0 8 0 1 100 64 0",
		"io":   procIOFixture,
	})
	// Non-numeric entries in /proc (self, sys, meminfo) must be skipped.
	write("self", map[string]string{"stat": procStatFixture})
	if err := os.WriteFile(filepath.Join(root, "meminfo"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	old := procRoot
	procRoot = root
	defer func() { procRoot = old }()

	entries, err := readProcesses()
	if err != nil {
		t.Fatalf("readProcesses: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 (pids 100 and 200 only)", len(entries))
	}

	byPID := map[int]entry{}
	for _, e := range entries {
		byPID[e.pid] = e
	}
	shell, ok := byPID[100]
	if !ok {
		t.Fatal("pid 100 missing")
	}
	if !shell.cumulative {
		t.Error("linux entries must be marked cumulative")
	}
	if want := int64(12800) * int64(os.Getpagesize()); shell.rssBytes != want {
		t.Errorf("pid 100 rssBytes = %d, want %d", shell.rssBytes, want)
	}
	if shell.ioOK {
		t.Error("pid 100 has no io file; ioOK must stay false")
	}

	agent := byPID[200]
	if agent.ppid != 100 {
		t.Errorf("pid 200 ppid = %d, want 100", agent.ppid)
	}
	if !agent.ioOK || agent.ioReadBytes != 2097152 {
		t.Errorf("pid 200 io = %+v, want readable with 2097152 read bytes", agent)
	}
}
