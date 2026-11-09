//go:build linux

package hostvitals

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadFromFilesAndATolerantPSI(t *testing.T) {
	dir := t.TempDir()
	mi := filepath.Join(dir, "meminfo")
	if err := os.WriteFile(mi, []byte(meminfoSample), 0o600); err != nil {
		t.Fatal(err)
	}

	// No PSI file, as on a kernel built without it: swap still reads and the
	// pressure group stays a gap.
	r := readFrom(mi, filepath.Join(dir, "absent"))
	if !r.SwapOK || r.SwapTotal != 2097152<<10 || r.PSIOK {
		t.Errorf("readFrom with no PSI file = %+v", r)
	}

	psi := filepath.Join(dir, "memory")
	if err := os.WriteFile(psi, []byte("some avg10=2.00 avg60=0 avg300=0 total=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r = readFrom(mi, psi)
	if !r.PSIOK || r.PSISomeAvg10 != 2 {
		t.Errorf("readFrom with a PSI file = %+v", r)
	}
}

// Read is the real host. It must not panic, and a Linux host with /proc has
// swap lines in meminfo.
func TestReadOnThisHost(t *testing.T) {
	if _, err := os.Stat("/proc/meminfo"); err != nil {
		t.Skip("no /proc/meminfo")
	}
	if r := Read(); !r.SwapOK {
		t.Errorf("Read() = %+v, want a swap reading from /proc/meminfo", r)
	}
}
