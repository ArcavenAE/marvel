//go:build darwin

package procstat

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakePS puts a `ps` first on PATH that logs each call's arguments, one line
// per call, and answers the wide table (when asked for etime) or the narrow
// one. It returns the log's path.
func fakePS(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\n" +
		"echo \"$*\" >> '" + log + "'\n" +
		"case \"$*\" in\n" +
		"*etime=*)\n" +
		"  echo '    1     0   0.0      4096 07-05:13:12  0:01.50 launchd'\n" +
		"  echo '  300     1   2.5   1048576    16:19:11 424:10.93 Google Chrome He'\n" +
		"  echo '  301   300   0.0      2048       00:05  0:00.01 sleep'\n" +
		"  ;;\n" +
		"*)\n" +
		"  echo '    1     0   0.0      4096'\n" +
		"  echo '  300     1   2.5   1048576'\n" +
		"  ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(dir, "ps"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func psCalls(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// On macOS `ps -o comm=` prints argv[0], which any process can rewrite, so it
// can carry arguments. The wide read asks for ucomm, the executable's own name,
// as the last column, and for no comm column.
func TestWideReadAsksPsForTheExecutableName(t *testing.T) {
	log := fakePS(t)
	procs, err := ReadTable()
	if err != nil {
		t.Fatalf("ReadTable: %v", err)
	}
	calls := psCalls(t, log)
	if len(calls) != 1 {
		t.Fatalf("ps was called %d times, want 1: %q", len(calls), calls)
	}
	args := strings.Fields(calls[0])
	cols := args[len(args)-1]
	if !strings.HasSuffix(cols, ",ucomm=") {
		t.Errorf("ps columns = %q, want the last to be ucomm=", cols)
	}
	for _, c := range strings.Split(cols, ",") {
		if c == "comm=" || c == "command=" {
			t.Errorf("ps columns = %q, want no %s column (argv[0] can hold arguments)", cols, c)
		}
	}
	if len(procs) != 3 || procs[1].Name != "Google Chrome He" {
		t.Errorf("procs = %+v, want three, with the spaced name kept whole", procs)
	}
}

// A process that rewrites its argv[0] to carry arguments shows its executable
// name, never the argv text, in the table the vitals read. This runs the real
// ps; where ps cannot run (a sandbox that refuses the setuid binary) it skips.
func TestRealTableNameIsTheExecutableNotArgvZero(t *testing.T) {
	if _, err := ReadTable(); err != nil {
		t.Skipf("the process table cannot be read here: %v", err)
	}
	cmd := exec.Command("/bin/bash", "-c", `exec -a "fake-tool --token=X1" /bin/sleep 20`)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	var got *Proc
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		procs, err := ReadTable()
		if err != nil {
			t.Fatalf("ReadTable: %v", err)
		}
		got = nil
		for i := range procs {
			if procs[i].PID == cmd.Process.Pid && procs[i].Name != "bash" {
				got = &procs[i]
			}
		}
		if got != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got == nil {
		t.Fatal("the child never showed its exec'd name in the table")
	}
	if got.Name != "sleep" || strings.Contains(got.Name, "token") || strings.Contains(got.Name, "fake-tool") {
		t.Errorf("name = %q, want the executable name sleep with no argv text", got.Name)
	}
}
