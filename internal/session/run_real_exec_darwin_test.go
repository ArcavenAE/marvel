//go:build darwin

package session

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests run the real /usr/bin/sandbox-exec on profiles ComposeRunProfile
// builds. A run already inside a seat's profile cannot start one (macOS refuses
// an inner profile that differs from the outer one, rc 71), so they skip there;
// they are meant to be run from a shell outside any profile (marvel#1596).
//
//	go test ./internal/session -run TestRealExec -v -count=1

const runRealSandboxExec = "/usr/bin/sandbox-exec"

func realExec(t *testing.T, profile string, argv ...string) (rc int, out string) {
	t.Helper()
	f := filepath.Join(t.TempDir(), "p.sb")
	if err := os.WriteFile(f, []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := RunChild(context.Background(), &RunPlan{Prefix: []string{runRealSandboxExec, "-f", f}}, argv, "/",
		[]string{"PATH=/usr/bin:/bin"}, 20*time.Second, 1<<16)
	if err != nil {
		t.Fatalf("run %v: %v", argv, err)
	}
	return res.ExitCode, res.Stdout + res.Stderr
}

func skipIfNested(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(runRealSandboxExec); err != nil {
		t.Skip("no sandbox-exec")
	}
	// The probe runs sandbox-exec directly, not through RunChild, so a broken
	// RunChild fails the tests below and does not make them skip or run bare.
	out, err := exec.Command(runRealSandboxExec, "-p", "(version 1)(allow default)", "/usr/bin/true").CombinedOutput()
	if err != nil {
		t.Skipf("sandbox-exec cannot start from here (already inside a profile?): %v: %q", err, out)
	}
}

func scratchDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func listenLocal(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

func nc(port int) []string {
	return []string{"/usr/bin/nc", "-z", "-w", "3", "127.0.0.1", fmt.Sprint(port)}
}

// A composed deny-only run blocks what it names and leaves the rest alone, and
// the deny block that follows it holds.
func TestRealExecComposedDenyHolds(t *testing.T) {
	skipIfNested(t)
	denied, free, blocked := scratchDir(t), scratchDir(t), scratchDir(t)
	composed, err := ComposeRunProfile(allowRole, fmt.Sprintf("(deny file-write* (subpath %q))\n", denied),
		fmt.Sprintf("(deny file-read* file-write* (subpath %q))\n", blocked))
	if err != nil {
		t.Fatal(err)
	}
	if rc, out := realExec(t, composed, "/usr/bin/touch", filepath.Join(denied, "x")); rc == 0 {
		t.Errorf("a write under the requested deny succeeded: %s", out)
	}
	if rc, out := realExec(t, composed, "/usr/bin/touch", filepath.Join(blocked, "x")); rc == 0 {
		t.Errorf("a write under the deny block succeeded: %s", out)
	}
	if rc, out := realExec(t, composed, "/usr/bin/touch", filepath.Join(free, "x")); rc != 0 {
		t.Errorf("a write outside every deny failed: rc=%d %s", rc, out)
	}
}

// The run-conditions network form, as the endpoint accepts it, applies: a
// per-port deny placed last refuses that port and a sibling listener connects.
func TestRealExecRunConditionsNetworkForm(t *testing.T) {
	skipIfNested(t)
	deniedPort, okPort := listenLocal(t), listenLocal(t)
	req := strings.Join([]string{
		"(deny network*)",
		`(allow network* (remote ip "localhost:*"))`,
		`(allow network-bind (local ip "localhost:*"))`,
		`(allow network-inbound (local ip "localhost:*"))`,
		fmt.Sprintf(`(deny network-outbound (remote ip "localhost:%d"))`, deniedPort),
	}, "\n") + "\n"
	composed, err := ComposeRunProfile(allowRole, req, "(deny file-write* (subpath \"/nonexistent-run-block\"))\n")
	if err != nil {
		t.Fatalf("the run-conditions form was refused: %v", err)
	}
	if rc, out := realExec(t, composed, nc(okPort)...); rc != 0 {
		t.Errorf("a localhost connect the form allows failed: rc=%d %s", rc, out)
	}
	if rc, out := realExec(t, composed, nc(deniedPort)...); rc == 0 {
		t.Errorf("the per-port deny did not refuse port %d: %s", deniedPort, out)
	}
	if rc, out := realExec(t, composed, "/usr/bin/nc", "-z", "-w", "3", "192.0.2.1", "80"); rc == 0 {
		t.Errorf("a connect to 192.0.2.1:80 succeeded under (deny network*): %s", out)
	}
}

// Probe, not a contract: a role that denies a local port, with the localhost
// allow placed after it, as a naive concatenation would. The endpoint refuses
// that composition (R5); this records what the kernel does with it, so the
// refusal rests on a measurement.
func TestRealExecProbeRoleDenyAgainstLocalhostAllow(t *testing.T) {
	skipIfNested(t)
	port := listenLocal(t)
	role := fmt.Sprintf("(version 1)\n(allow default)\n(deny network-outbound (remote ip \"localhost:%d\"))\n", port)
	allow := `(allow network* (remote ip "localhost:*"))` + "\n"
	if _, err := ComposeRunProfile(role, allow, ""); !errors.Is(err, ErrRunProfileRefused) {
		t.Errorf("the composer accepted a localhost allow over a role's network deny: %v", err)
	}
	rcRole, _ := realExec(t, role, nc(port)...)
	rcNaive, _ := realExec(t, role+allow, nc(port)...)
	t.Logf("PROBE role-deny-vs-allow: role alone rc=%d (want nonzero: denied); role then allow rc=%d (0 means the allow lifted the role's deny)", rcRole, rcNaive)
	if rcRole == 0 {
		t.Errorf("the control failed: the role's own deny did not refuse port %d", port)
	}
}

// Probe, not a contract: does rebinding deny make a later deny a no-op? The
// composer refuses a define, so this only records the kernel's answer for the
// record (architect finding R4c).
func TestRealExecProbeDefineRebindsDeny(t *testing.T) {
	skipIfNested(t)
	dir := scratchDir(t)
	req := "(define (deny . a) #t)\n"
	if _, err := ComposeRunProfile(allowRole, req, ""); !errors.Is(err, ErrRunProfileRefused) {
		t.Errorf("the composer accepted a define: %v", err)
	}
	prof := fmt.Sprintf("(version 1)\n(allow default)\n%s(deny file-write* (subpath %q))\n", req, dir)
	rc, out := realExec(t, prof, "/usr/bin/touch", filepath.Join(dir, "x"))
	t.Logf("PROBE define-rebinds-deny: rc=%d out=%q (0 means a define neutralised the later deny; nonzero means the profile was refused or the deny held)", rc, strings.TrimSpace(out))
}
