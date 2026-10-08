package bus

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// environFromProc reads a process environment from a /proc/<pid>/environ file,
// which holds NUL-separated KEY=value entries, and returns it one entry per
// line.
func environFromProc(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, entry := range strings.Split(string(data), "\x00") {
		if entry != "" {
			b.WriteString(entry + "\n")
		}
	}
	return b.String(), nil
}

// brokerEnviron returns the environment of a running process: /proc where the
// kernel offers it (Linux), else ps -E (BSD and macOS, whose ps has the flag).
func brokerEnviron(pid int) (string, error) {
	if env, err := environFromProc(fmt.Sprintf("/proc/%d/environ", pid)); err == nil {
		return env, nil
	}
	out, err := exec.Command("ps", "-E", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil || !strings.Contains(string(out), "=") {
		return "", fmt.Errorf("cannot read the process environment from the kernel here (%v)", err)
	}
	return string(out), nil
}

func TestEnvironFromProcSplitsNULSeparatedEntries(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "environ")
	if err := os.WriteFile(path, []byte("A=1\x00MARVEL_TEST_MINTED=1\x00EMPTY=\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := environFromProc(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "A=1\nMARVEL_TEST_MINTED=1\nEMPTY=\n"; got != want {
		t.Errorf("environ = %q, want %q", got, want)
	}
}

func TestEnvironFromProcReportsAMissingFile(t *testing.T) {
	t.Parallel()
	if _, err := environFromProc(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("a missing environ file read without error")
	}
}

// TestBrokerEnvironmentExcludesOperatorSecrets: the daemon's environment
// carries whatever the operator's shell exported (the bd client password,
// a heartbeat token). The broker is spawned through workload.Start with the
// message-bus allowlist, so none of it reaches the broker; the minted seed
// still does (services-list.md section 3.4, test 3; aae-orc-oo62t).
func TestBrokerEnvironmentExcludesOperatorSecrets(t *testing.T) {
	t.Setenv("BEADS_DOLT_PASSWORD", "operator-secret")
	t.Setenv("MARVEL_HEARTBEAT_TOKEN", "operator-token")
	s, _, _ := newTestSupervisor(t, "")
	s.Env = func() []string { return []string{"MARVEL_TEST_MINTED=1"} }
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	env, err := brokerEnviron(s.Status().PID)
	if err != nil {
		t.Skipf("cannot read the broker's environment: %v", err)
	}
	if !strings.Contains(env, "MARVEL_TEST_MINTED=1") {
		t.Errorf("broker environment lacks the minted variable:\n%s", env)
	}
	for _, k := range []string{"BEADS_DOLT_PASSWORD", "MARVEL_HEARTBEAT_TOKEN"} {
		if strings.Contains(env, k+"=") {
			t.Errorf("broker environment carries %s:\n%s", k, env)
		}
	}
}
