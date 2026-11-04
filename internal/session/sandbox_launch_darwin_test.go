//go:build darwin

package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These run the real /usr/bin/sandbox-exec. A control run first: if the host will
// not let this process apply a profile at all (it is itself sandboxed), the tests
// skip with that reason instead of reading a refusal as a pass.

func realSandboxExec(t *testing.T) string {
	t.Helper()
	const exe = "/usr/bin/sandbox-exec"
	if _, err := os.Stat(exe); err != nil {
		t.Skip("no sandbox-exec")
	}
	out, err := exec.Command(exe, "-p", "(version 1)(allow default)", "/usr/bin/true").CombinedOutput()
	if err != nil {
		t.Skipf("this process cannot apply a sandbox profile: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return exe
}

// The argument shape wrapInSandbox builds runs: -f a profile file, -D parameters
// the profile reads, then the command.
func TestRealSandboxExecRunsTheWrappedShape(t *testing.T) {
	exe := realSandboxExec(t)
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	profile := filepath.Join(dir, "p.sb")
	if err := os.WriteFile(profile, []byte("(version 1)\n(allow default)\n(deny file-read* (subpath (param \"DENIED\")))\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	denied := filepath.Join(dir, "denied")
	if err := os.Mkdir(denied, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(denied, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command(exe, "-f", profile, "-D", "DENIED="+denied, "/usr/bin/true").Run(); err != nil {
		t.Fatalf("the wrapped shape did not run: %v", err)
	}
	if err := exec.Command(exe, "-f", profile, "-D", "DENIED="+denied, "/bin/cat", filepath.Join(denied, "f")).Run(); err == nil {
		t.Error("a read inside the denied tree succeeded")
	}
}

// Order matters: the real home is denied before the trees carved back out of it,
// so a read under REALHOME but outside the two allowed trees is refused while a
// read inside WORKDIR succeeds. The profile is the example's rule order in a
// minimal form that can exec cat.
func TestRealSandboxExecKeepsTheRealHomeDeniedBeforeTheCarveOuts(t *testing.T) {
	exe := realSandboxExec(t)
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	realHome := filepath.Join(dir, "home")
	work := filepath.Join(realHome, "work")
	other := filepath.Join(realHome, "other")
	for _, d := range []string{work, other} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{filepath.Join(work, "in"), filepath.Join(other, "out")} {
		if err := os.WriteFile(f, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	profile := filepath.Join(dir, "p.sb")
	body := `(version 1)
(deny default)
(allow process-exec (literal "/bin/cat"))
(allow process-fork)
(allow sysctl-read)
(allow file-read-metadata)
(deny file-read* (subpath (param "REALHOME")))
(allow file-read* (literal "/") (subpath "/usr/lib") (subpath "/usr/share") (subpath "/System")
  (subpath "/private/etc") (subpath "/private/var/db/timezone") (subpath "/dev") (literal "/bin/cat"))
(allow file-read* (subpath (param "WORKDIR")))
`
	if err := os.WriteFile(profile, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(file string) error {
		return exec.Command(exe, "-f", profile, "-D", "REALHOME="+realHome, "-D", "WORKDIR="+work, "/bin/cat", file).Run()
	}
	if err := run(filepath.Join(work, "in")); err != nil {
		t.Skipf("the minimal profile cannot run cat on this host, so the order cannot be shown: %v", err)
	}
	if err := run(filepath.Join(other, "out")); err == nil {
		t.Error("a read under REALHOME but outside WORKDIR succeeded")
	}
}
