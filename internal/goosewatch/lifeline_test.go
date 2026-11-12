package goosewatch

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// leakParent is the "leakparent" mode of this binary: it starts a wrapper over
// a goose that sleeps, writes the wrapper's pid, and then waits to be killed.
// The arguments are leakparent <dir>.
func leakParent(dir string) int {
	exe, err := os.Executable()
	if err != nil {
		return 94
	}
	tok := newToken()
	script := fmt.Sprintf("echo $$ > %s; sleep 600", filepath.Join(dir, "goose.pid"))
	cmd := exec.Command(exe, reexecFlag, tok, "wrapper", filepath.Join(dir, "stderr.log"), "1s", script)
	cmd.Env = append(os.Environ(), reexecEnv+"="+tok)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return 94
	}
	if err := os.WriteFile(filepath.Join(dir, "wrapper.pid"), []byte(fmt.Sprint(cmd.Process.Pid)), 0o600); err != nil {
		return 94
	}
	for pidOf(filepath.Join(dir, "goose.pid")) < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	_ = os.WriteFile(filepath.Join(dir, "ready"), nil, 0o600)
	time.Sleep(10 * time.Minute)
	return 0
}

// pidOf reads a pid file without a *testing.T, for the parent mode.
func pidOf(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

func processGone(pid int) bool {
	return syscall.Kill(pid, 0) == syscall.ESRCH
}

// runLeak starts leakParent as a re-exec, waits until it is ready, SIGKILLs it,
// and returns the pids of the wrapper and of goose that it left.
func runLeak(t *testing.T) (wrapper, goose int) {
	t.Helper()
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tok := newToken()
	parent := exec.Command(exe, reexecFlag, tok, "leakparent", dir)
	parent.Env = append(os.Environ(), reexecEnv+"="+tok)
	parent.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-parent.Process.Pid, syscall.SIGKILL)
		_ = parent.Wait()
		// A leftover of a failed run: end what this test made, by pid.
		for _, name := range []string{"wrapper.pid", "goose.pid"} {
			if pid := readPid(t, filepath.Join(dir, name)); pid > 1 {
				_ = syscall.Kill(-pid, syscall.SIGCONT)
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		}
	})
	waitFor(t, "the parent to be ready", 10*time.Second, func() bool {
		_, err := os.Stat(filepath.Join(dir, "ready"))
		return err == nil
	})
	wrapper = readPid(t, filepath.Join(dir, "wrapper.pid"))
	goose = readPid(t, filepath.Join(dir, "goose.pid"))
	if wrapper < 2 || goose < 2 {
		t.Fatalf("wrapper pid %d, goose pid %d, want both set", wrapper, goose)
	}
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = parent.Wait()
	return wrapper, goose
}

func assertNoSurvivors(t *testing.T, wrapper, goose int) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) && (!processGone(wrapper) || !groupGone(goose)) {
		time.Sleep(50 * time.Millisecond)
	}
	var left []string
	if !processGone(wrapper) {
		left = append(left, fmt.Sprintf("wrapper %d", wrapper))
	}
	if !groupGone(goose) {
		left = append(left, fmt.Sprintf("goose group %d", goose))
	}
	if len(left) > 0 {
		t.Errorf("after the test parent was SIGKILLed, still running 6s later: %s", strings.Join(left, ", "))
	}
}

// A test run that is SIGKILLed cannot run its cleanups. The wrapper it started
// must still end, and so must goose.
func TestReexecChildEndsWhenTheTestParentIsKilled(t *testing.T) {
	t.Parallel()
	w, g := runLeak(t)
	assertNoSurvivors(t, w, g)
}
