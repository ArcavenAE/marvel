package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// These tests run the real attach line, attach_exec.go's
// `tmux -L <socket> attach -t %<desk>`, against a real tmux server on a
// scratch socket. The channel tests elsewhere swap the command for
// /bin/sh "exec cat", so nothing else runs that line (i556r-4-c, c1).
//
// Mutants each of them kills, read against CI's green (nobody runs them on the
// host): "-L" to "-S" fails TestRealAttachBridgesATypedLine and every other
// test here (tmux cannot find the pane); a target with the "%" dropped fails
// all five tests here (each attaches to the desk, or reports its absence);
// socket and desk swapped fails all of them.

// realTmux starts a tmux server on a scratch socket with one pane running cat
// and returns a function that runs tmux against it, and the pane's desk number.
// It skips without tmux, like the tests beside it.
func realTmux(t *testing.T) (run func(args ...string) string, desk, socket string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
	// A short directory: tmux puts the socket under it and a unix socket path
	// is limited to about 100 bytes.
	dir, err := os.MkdirTemp("", "ra")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("TMUX", "")
	// A scratch HOME, for this helper's tmux and the daemon side that passes
	// HOME through attachEnvAllow, so a user's ~/.tmux.conf cannot change the
	// pane size or window-size option the tests read.
	t.Setenv("HOME", t.TempDir())
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	socket = "ra" + hex.EncodeToString(b)
	run = func(args ...string) string {
		cmd := exec.Command("tmux", append([]string{"-L", socket}, args...)...)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "TMUX_TMPDIR=" + dir, "TERM=xterm"}
		out, _ := cmd.CombinedOutput()
		return strings.TrimSpace(string(out))
	}
	if out := run("new-session", "-d", "-x", "80", "-y", "24", "-s", "a", "cat"); out != "" {
		t.Fatalf("tmux new-session: %s", out)
	}
	t.Cleanup(func() { run("kill-server") })
	pane := run("display-message", "-p", "-t", "a", "#{pane_id}")
	if !strings.HasPrefix(pane, "%") {
		t.Fatalf("pane id = %q, want one starting with %%", pane)
	}
	return run, strings.TrimPrefix(pane, "%"), socket
}

// realAttachRig is an attach listener whose command is the production
// attachCommand against the scratch server, whatever socket the resolver names.
func realAttachRig(t *testing.T, socket string) *attachRig {
	t.Helper()
	gate := true
	return newAttachRigSeam(t, ScopeAdmin, &gate, nil, func(_, desk string) *exec.Cmd {
		return attachCommand(socket, desk)
	})
}

// openReal opens an attach channel for desk with a pty of the given size and
// starts the shell.
func openReal(t *testing.T, r *attachRig, desk string, cols, rows uint32) ssh.Channel {
	t.Helper()
	ch, reqs, err := openAttach(r.client, desk)
	if err != nil {
		t.Fatalf("open attach channel for desk %s: %v", desk, err)
	}
	go ssh.DiscardRequests(reqs)
	t.Cleanup(func() { _ = ch.Close() })
	ok, err := ch.SendRequest("pty-req", true, ssh.Marshal(ptyReq{Term: "xterm", Cols: cols, Rows: rows}))
	if err != nil || !ok {
		t.Fatalf("pty-req: ok=%v err=%v", ok, err)
	}
	ok, err = ch.SendRequest("shell", true, nil)
	if err != nil || !ok {
		t.Fatalf("shell: ok=%v err=%v", ok, err)
	}
	return ch
}

// eventually polls f until it returns true or the deadline passes.
func eventually(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s did not hold within 10s", what)
}

// A line typed on the channel reaches the pane's cat through a real tmux
// client and comes back on the channel, and the pane takes the client's size.
func TestRealAttachBridgesATypedLine(t *testing.T) {
	run, desk, socket := realTmux(t)
	r := realAttachRig(t, socket)
	ch := openReal(t, r, desk, 90, 30)
	eventually(t, "a tmux client attached", func() bool { return run("list-clients") != "" })
	eventually(t, "the pane at the client's width", func() bool {
		return run("display-message", "-p", "-t", "%"+desk, "#{pane_width}") == "90"
	})
	if _, err := ch.Write([]byte("real-attach-line\r")); err != nil {
		t.Fatalf("write: %v", err)
	}
	readUntil(t, ch, "real-attach-line", 10*time.Second)
}

// A window-change on the channel resizes the pane through the real client.
func TestRealAttachForwardsAWindowChange(t *testing.T) {
	run, desk, socket := realTmux(t)
	r := realAttachRig(t, socket)
	ch := openReal(t, r, desk, 80, 24)
	eventually(t, "a tmux client attached", func() bool { return run("list-clients") != "" })
	if _, err := ch.SendRequest("window-change", false, ssh.Marshal(attachWindowChange{Cols: 100, Rows: 30})); err != nil {
		t.Fatalf("window-change: %v", err)
	}
	eventually(t, "the pane at 100 columns", func() bool {
		return run("display-message", "-p", "-t", "%"+desk, "#{pane_width}") == "100"
	})
}

// Closing the channel detaches the client and leaves the pane running: the
// agent in it is not stopped by a closed attach.
func TestRealAttachCloseDetachesAndLeavesThePane(t *testing.T) {
	run, desk, socket := realTmux(t)
	r := realAttachRig(t, socket)
	ch := openReal(t, r, desk, 80, 24)
	eventually(t, "a tmux client attached", func() bool { return run("list-clients") != "" })
	if err := ch.Close(); err != nil {
		t.Fatalf("close channel: %v", err)
	}
	eventually(t, "the client detached", func() bool { return run("list-clients") == "" })
	if got := run("list-panes", "-a", "-F", "#{pane_id}"); !strings.Contains(got, "%"+desk) {
		t.Errorf("panes after the close = %q, want %%%s still there", got, desk)
	}
}

// A second attach after the first closed works with no repair, as after a
// sleep and wake (a new dial each time).
func TestRealAttachWorksAgainAfterAClose(t *testing.T) {
	run, desk, socket := realTmux(t)
	for i := 0; i < 2; i++ {
		r := realAttachRig(t, socket)
		ch := openReal(t, r, desk, 80, 24)
		eventually(t, "a tmux client attached", func() bool { return run("list-clients") != "" })
		if _, err := ch.Write([]byte("again\r")); err != nil {
			t.Fatalf("round %d write: %v", i, err)
		}
		readUntil(t, ch, "again", 10*time.Second)
		_ = ch.Close()
		eventually(t, "the client detached", func() bool { return run("list-clients") == "" })
	}
}

// A desk that is not a pane on that server: tmux says so on the channel and
// the channel ends, with no client attached anywhere.
func TestRealAttachOfAMissingDeskReportsAndCloses(t *testing.T) {
	run, _, socket := realTmux(t)
	r := realAttachRig(t, socket)
	ch := openReal(t, r, "9999", 80, 24)
	readUntil(t, ch, "can't find pane", 10*time.Second)
	if got := run("list-clients"); got != "" {
		t.Errorf("clients after attaching a missing desk = %q, want none", got)
	}
}
