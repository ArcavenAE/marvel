//go:build darwin

package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The deny block run by the real sandbox-exec, on temporary paths. The rig's
// fake sandbox-exec cannot show what the kernel does with the rules.

// runUnder runs sh -c cmd under the profile text and returns the exit code and output.
func runUnder(t *testing.T, bin, profile, cmd string) (int, string) {
	t.Helper()
	f := filepath.Join(t.TempDir(), "p.sb")
	if err := os.WriteFile(f, []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "-f", f, "/bin/sh", "-c", cmd).CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), string(out)
	}
	t.Fatalf("run: %v", err)
	return -1, ""
}

type tccTree struct{ home, work, other string }

func newTCCTree(t *testing.T) tccTree {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tr := tccTree{home: home, work: filepath.Join(home, "Documents", "code"), other: filepath.Join(home, "Documents", "other")}
	for _, d := range []string{tr.work, tr.other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return tr
}

// A role whose own profile denies writes to the working tree keeps that deny
// under the default block. The measured failure: with the block's allow after
// the role's deny, a write that the role profile alone refuses returned 0.
func TestARoleWriteDenyOnTheWorkdirSurvivesTheBlock(t *testing.T) {
	bin := realSandboxExec(t)
	tr := newTCCTree(t)
	role := "(version 1)\n(allow default)\n(deny file-write* (subpath \"" + tr.work + "\"))\n"
	write := "touch '" + tr.work + "/x'"
	if rc, out := runUnder(t, bin, role, write); rc == 0 {
		t.Fatalf("control: the role profile alone does not refuse the write (rc %d, %s)", rc, out)
	}
	staged := role + "\n" + tccDenyBlock(tr.home, nil, []string{tr.work})
	if rc, out := runUnder(t, bin, staged, write); rc == 0 {
		t.Errorf("the block lifted the role's own write deny on the workdir (rc %d, %s)", rc, out)
	}
}

// With no role deny the carve-out still gives the seat its tree, and the rest
// of the folder stays refused.
func TestTheCarveOutKeepsTheWorkdirUsableAndTheFolderDenied(t *testing.T) {
	bin := realSandboxExec(t)
	tr := newTCCTree(t)
	staged := "(version 1)\n(allow default)\n" + tccDenyBlock(tr.home, nil, []string{tr.work})
	if rc, out := runUnder(t, bin, staged, "touch '"+tr.work+"/ok' && ls '"+tr.work+"'"); rc != 0 {
		t.Errorf("the workdir is not usable under the block (rc %d, %s)", rc, out)
	}
	rc, out := runUnder(t, bin, staged, "ls '"+tr.other+"'")
	if rc == 0 || !strings.Contains(out, "Operation not permitted") {
		t.Errorf("the rest of the folder is readable (rc %d, %s)", rc, out)
	}
}
