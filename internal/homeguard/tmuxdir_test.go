package homeguard

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestChildReportsTmuxDir runs only as a child of
// TestChildTmuxTmpdirIsAFreshScratchDirNotTheParents.
func TestChildReportsTmuxDir(t *testing.T) {
	if childMode != "tmux" {
		t.Skip("child only")
	}
	dir, ok := os.LookupEnv("TMUX_TMPDIR")
	if !ok {
		t.Fatal("TMUX_TMPDIR is not set in a guarded binary")
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("TMUX_TMPDIR %q: %v", dir, err)
	}
	t.Logf("CHILD:TMUX_TMPDIR=%s", dir)
	t.Logf("CHILD:MODE=%o", fi.Mode().Perm())
	for _, name := range []string{"TMUX", "TMUX_PANE"} {
		if v, ok := os.LookupEnv(name); ok {
			t.Errorf("%s=%q is set in a guarded binary", name, v)
		}
	}
}

// A guarded binary started with a live tmux's TMUX_TMPDIR, TMUX and TMUX_PANE in
// its environment must not hold the first as it was, and must not hold the other
// two at all: its TMUX_TMPDIR is a fresh 0700 directory under the temp dir, short
// enough for a socket path, and gone when the binary exits.
func TestChildTmuxTmpdirIsAFreshScratchDirNotTheParents(t *testing.T) {
	const sentinel = "/sentinel/live-tmux-dir"
	cmd := exec.Command(os.Args[0], "-test.run=^TestChildReportsTmuxDir$", "-test.v")
	cmd.Env = []string{
		"HOMEGUARD_CHILD=tmux", "HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH"), "TMPDIR=" + os.TempDir(),
		"TMUX_TMPDIR=" + sentinel, "TMUX=/sentinel/live-tmux-dir/default,1,0", "TMUX_PANE=%1",
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	o := string(out)
	if !strings.Contains(o, "--- PASS: TestChildReportsTmuxDir") {
		t.Fatalf("the child test did not run to a pass:\n%s", o)
	}
	dir := childValue(o, "TMUX_TMPDIR")
	switch {
	case dir == "":
		t.Fatalf("the child reported no TMUX_TMPDIR:\n%s", o)
	case dir == sentinel || strings.HasPrefix(dir, sentinel):
		t.Errorf("the child kept the parent's TMUX_TMPDIR %q", dir)
	case !strings.HasPrefix(dir, os.TempDir()) && !strings.HasPrefix(dir, "/tmp/"):
		t.Errorf("TMUX_TMPDIR %q is outside the temp dir %q", dir, os.TempDir())
	}
	if len(dir) > tmuxSocketBudget {
		t.Errorf("TMUX_TMPDIR %q is %d bytes, over the %d a socket path leaves room for", dir, len(dir), tmuxSocketBudget)
	}
	if got := childValue(o, "MODE"); got != "700" {
		t.Errorf("TMUX_TMPDIR mode = %s, want 700", got)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the child left %q behind (%v)", dir, err)
	}
}
