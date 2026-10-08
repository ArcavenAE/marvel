package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// The start guard reads the pidfile with the same parser as every other reader
// (internal/pidfile), and probes only a pid that parser accepts. A number wider
// than 32 bits wraps in kill(2) to another process: this one, -1 (every
// process) or -5 (a process group). The probe is a seam, so no test signals a
// pid it did not start.
func withProbe(t *testing.T, fn func(int) error) *[]int {
	t.Helper()
	var probed []int
	old := signalPID
	signalPID = func(pid int) error { probed = append(probed, pid); return fn(pid) }
	t.Cleanup(func() { signalPID = old })
	return &probed
}

func pidfileWith(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "marvel.pid")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheckPidFileFreeNeverProbesAPidItCannotName(t *testing.T) {
	for name, content := range map[string]string{
		"wider than 32 bits": "4294967297\n",
		"wraps to -1":        "4294967295\n",
		"wraps to -5":        "4294967291\n",
		"trailing text":      "12abc\n",
		"negative":           "-5\n",
		"zero":               "0\n",
		"two numbers":        "4242 99\n",
		"empty":              "",
		"past int32":         "2147483648\n",
	} {
		probed := withProbe(t, func(int) error { return nil }) // every probe would say alive
		if err := checkPidFileFree(pidfileWith(t, content)); err != nil {
			t.Errorf("%s: a pidfile that names no process refused the start: %v", name, err)
		}
		if len(*probed) != 0 {
			t.Errorf("%s: probed pid(s) %v from a pidfile that names none", name, *probed)
		}
	}
}

func TestCheckPidFileFreeStillRefusesALiveProcess(t *testing.T) {
	probed := withProbe(t, func(int) error { return nil })
	err := checkPidFileFree(pidfileWith(t, "4242\n"))
	if err == nil || !strings.Contains(err.Error(), "names live process 4242") {
		t.Fatalf("want the live-process refusal, got %v", err)
	}
	if fmt.Sprint(*probed) != "[4242]" {
		t.Errorf("probed %v, want exactly [4242]", *probed)
	}
	// A dead process, and a pid the caller may not signal that does not exist
	// to it, leave the pidfile stale.
	withProbe(t, func(int) error { return syscall.ESRCH })
	if err := checkPidFileFree(pidfileWith(t, "4242\n")); err != nil {
		t.Errorf("a stale pidfile refused the start: %v", err)
	}
	// A missing pidfile is no refusal.
	if err := checkPidFileFree(filepath.Join(t.TempDir(), "absent.pid")); err != nil {
		t.Errorf("a missing pidfile refused the start: %v", err)
	}
}
