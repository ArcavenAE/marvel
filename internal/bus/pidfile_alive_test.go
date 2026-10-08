package bus

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// The nats-server pidfile is read with the same parser as every other pidfile
// (internal/pidfile). A number wider than 32 bits wraps in kill(2) to another
// process (this one, -1 for every process, -5 for a process group), and the
// adopt path goes on to signal the pid it was given, so a wrapped value must
// count as not alive. The probe is a seam, so no test signals a process.
func recordProbes(t *testing.T, fn func(int) error) *[]int {
	t.Helper()
	var probed []int
	old := probePID
	probePID = func(pid int) error { probed = append(probed, pid); return fn(pid) }
	t.Cleanup(func() { probePID = old })
	return &probed
}

func pidfileText(t *testing.T, content string) *Supervisor {
	t.Helper()
	p := filepath.Join(t.TempDir(), "nats-server.pid")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return &Supervisor{pidFile: p}
}

func TestPidFileAliveNeverProbesAPidItCannotName(t *testing.T) {
	for name, content := range map[string]string{
		"wider than 32 bits": "4294967297\n",
		"wraps to -1":        "4294967295\n",
		"wraps to -5":        "4294967291\n",
		"trailing text":      "12abc\n",
		"negative":           "-5\n",
		"minus one":          "-1\n",
		"zero":               "0\n",
		"two numbers":        "4242 99\n",
		"empty":              "",
		"past int32":         "2147483648\n",
	} {
		probed := recordProbes(t, func(int) error { return nil }) // every probe would say alive
		if pid, alive := pidfileText(t, content).pidFileAlive(); alive || pid != 0 {
			t.Errorf("%s: a pidfile that names no process read as alive pid %d", name, pid)
		}
		if len(*probed) != 0 {
			t.Errorf("%s: probed pid(s) %v from a pidfile that names none", name, *probed)
		}
	}
}

func TestPidFileAliveStillReadsAValidPid(t *testing.T) {
	probed := recordProbes(t, func(int) error { return nil })
	if pid, alive := pidfileText(t, "4242\n").pidFileAlive(); !alive || pid != 4242 {
		t.Fatalf("a live pid read as %d, %v", pid, alive)
	}
	if len(*probed) != 1 || (*probed)[0] != 4242 {
		t.Errorf("probed %v, want exactly [4242]", *probed)
	}
	recordProbes(t, func(int) error { return syscall.ESRCH })
	if _, alive := pidfileText(t, "4242\n").pidFileAlive(); alive {
		t.Error("a dead pid read as alive")
	}
	if _, alive := (&Supervisor{pidFile: filepath.Join(t.TempDir(), "absent.pid")}).pidFileAlive(); alive {
		t.Error("a missing pidfile read as alive")
	}
}
