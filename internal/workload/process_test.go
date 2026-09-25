package workload

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	canary    = "MARVEL_TEST_CANARY"
	helperEnv = "WORKLOAD_TEST_HELPER"
)

// TestHelperProcess is not a test: it is the long-running child the kernel
// environment test spawns, and it returns at once in a normal run.
func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) != "sleep" {
		return
	}
	time.Sleep(30 * time.Second)
	os.Exit(0)
}

// TestStartChildEnvIsAllowlistPlusMinted: a child that prints its
// environment sees the allowed key and the minted secret, and nothing else
// the test process carries (services-list.md section 3.4, test 1).
func TestStartChildEnvIsAllowlistPlusMinted(t *testing.T) {
	t.Setenv(canary, "leaked")
	env, err := exec.LookPath("env")
	if err != nil {
		t.Skip("no env binary")
	}
	logPath := filepath.Join(t.TempDir(), "child.log")
	child, err := Start(ProcessSpec{
		Name:      "test",
		Binary:    env,
		EnvAllow:  []string{"PATH"},
		MintedEnv: func() []string { return []string{"MINTED=1"} },
		LogPath:   logPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Cmd.Wait(); err != nil {
		t.Fatalf("env child: %v", err)
	}
	out, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if k, _, ok := strings.Cut(line, "="); ok {
			got[k] = true
		}
	}
	if !got["MINTED"] || !got["PATH"] {
		t.Errorf("child env lacks MINTED or PATH:\n%s", out)
	}
	if len(got) != 2 {
		t.Errorf("child env has %d keys, want exactly PATH and MINTED:\n%s", len(got), out)
	}
	for _, k := range []string{canary, "HOME"} {
		if got[k] {
			t.Errorf("child env carries %s, which is not allowed", k)
		}
	}
}

// TestStartLongRunningChildEnvFromKernel: the same property read from the
// kernel for a child that is still running, the way the bus seed test reads
// a live broker (section 3.4, test 2).
func TestStartLongRunningChildEnvFromKernel(t *testing.T) {
	t.Setenv(canary, "leaked")
	// The test binary is the child: macOS withholds the environment of
	// Apple platform binaries (/bin/sleep) from ps -E, not of our own.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "run", "child.pid")
	child, err := Start(ProcessSpec{
		Name:      "test",
		Binary:    self,
		Args:      []string{"-test.run=^TestHelperProcess$"},
		EnvAllow:  []string{"PATH"},
		MintedEnv: func() []string { return []string{"MINTED=1", helperEnv + "=sleep"} },
		LogPath:   filepath.Join(dir, "log", "child.log"),
		PidFile:   pidFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-child.Pid, syscall.SIGKILL)
		_ = child.Cmd.Wait()
	})
	b, err := os.ReadFile(pidFile)
	if err != nil || strings.TrimSpace(string(b)) != strconv.Itoa(child.Pid) {
		t.Errorf("pidfile = %q (%v), want %d", b, err, child.Pid)
	}
	environ := kernelEnviron(t, child.Pid)
	if !strings.Contains(environ, "MINTED=1") {
		t.Errorf("running child lacks MINTED=1:\n%s", environ)
	}
	if strings.Contains(environ, canary) {
		t.Errorf("running child carries %s:\n%s", canary, environ)
	}
}

// TestStartCheckRefusesBeforeSpawn: a Check error stops the spawn and is
// returned as is, and no log file is created for a child that never ran.
func TestStartCheckRefusesBeforeSpawn(t *testing.T) {
	t.Parallel()
	refused := errors.New("refused")
	logPath := filepath.Join(t.TempDir(), "child.log")
	var seen []string
	_, err := Start(ProcessSpec{
		Name:      "test",
		Binary:    "/nonexistent",
		MintedEnv: func() []string { return []string{"MINTED=1"} },
		Check: func(env []string) error {
			seen = env
			return refused
		},
		LogPath: logPath,
	})
	if !errors.Is(err, refused) {
		t.Fatalf("err = %v, want the Check error", err)
	}
	if len(seen) != 1 || seen[0] != "MINTED=1" {
		t.Errorf("Check saw %v, want [MINTED=1]", seen)
	}
	if _, serr := os.Stat(logPath); !os.IsNotExist(serr) {
		t.Errorf("log file exists for a refused spawn: %v", serr)
	}
}

func TestChildEnv(t *testing.T) {
	t.Setenv("WL_A", "a")
	t.Setenv("WL_B", "b")
	tests := []struct {
		name          string
		allow, minted []string
		want          []string
	}{
		{"nothing", nil, nil, []string{}},
		{"allowed present", []string{"WL_A"}, nil, []string{"WL_A=a"}},
		{"allowed absent skipped", []string{"WL_UNSET_KEY"}, nil, []string{}},
		{"minted after allowed", []string{"WL_A", "WL_B"}, []string{"M=1"}, []string{"WL_A=a", "WL_B=b", "M=1"}},
		{"minted repeats allowed and comes last", []string{"WL_A"}, []string{"WL_A=override"}, []string{"WL_A=a", "WL_A=override"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := childEnv(tt.allow, tt.minted)
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("childEnv(%v, %v) = %v, want %v", tt.allow, tt.minted, got, tt.want)
			}
		})
	}
}

// kernelEnviron reads a running process's environment from the kernel:
// /proc on Linux, ps -E on macOS. It retries briefly, because a just-forked
// child may not have exec'd yet, and skips where neither source answers.
func kernelEnviron(t *testing.T, pid int) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ"); err == nil {
			if s := strings.ReplaceAll(string(b), "\x00", "\n"); strings.Contains(s, "=") {
				return s
			}
		} else if out, perr := exec.Command("ps", "-E", "-o", "command=", "-p", strconv.Itoa(pid)).Output(); perr == nil {
			if s := string(out); strings.Contains(s, helperEnv+"=") {
				return s
			}
		} else if time.Now().After(deadline) {
			t.Skipf("cannot read the environment of pid %d on this platform", pid)
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d environment never readable", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
