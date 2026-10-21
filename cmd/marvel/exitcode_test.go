package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/knownhosts"
)

// TestConnectErrorPrintsNoUsage: a one-shot read against a socket nothing
// listens on prints one line, no usage block, and exits 6 (unreachable).
func TestConnectErrorPrintsNoUsage(t *testing.T) {
	dir, err := os.MkdirTemp("", "ce")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	dead := filepath.Join(dir, "dead.sock")

	root := newRootCmd()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"get", "sessions", "--socket", dead})
	code := execute(root, &errOut)

	if code != exitUnreachable {
		t.Errorf("exit = %d, want %d (unreachable)", code, exitUnreachable)
	}
	text := errOut.String()
	if strings.Contains(text, "Usage:") {
		t.Errorf("stderr holds a usage block:\n%s", text)
	}
	if lines := strings.Split(strings.TrimRight(text, "\n"), "\n"); len(lines) != 1 || lines[0] == "" {
		t.Errorf("stderr = %d lines, want one:\n%s", len(lines), text)
	}
	if !strings.Contains(text, dead) {
		t.Errorf("stderr does not name the address %s:\n%s", dead, text)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
}

// TestUsageErrorStillPrintsUsage is the control for the test above: an
// error in how the command was typed keeps its usage block and exit 1.
func TestUsageErrorStillPrintsUsage(t *testing.T) {
	root := newRootCmd()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{"get", "sessions", "--no-such-flag"})
	code := execute(root, &errOut)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut.String()+out.String(), "Usage:") {
		t.Errorf("no usage block for a bad flag:\nstderr:\n%s\nstdout:\n%s", errOut.String(), out.String())
	}
}

// TestExitCodeTable: each typed error maps to one code, bare and wrapped.
func TestExitCodeTable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"bad reply", &daemonrpc.BadReplyError{Err: errors.New("x")}, 5},
		{"unreachable", &daemonrpc.UnreachableError{Addr: "a", Err: errors.New("x")}, 6},
		{"timeout", &daemonrpc.TimeoutError{Phase: "connect", Err: errors.New("x")}, 7},
		{"auth refused", &daemonrpc.AuthRefusedError{Addr: "a"}, 8},
		{"no identity", &daemonrpc.NoIdentityError{}, 8},
		{"host key unknown", &knownhosts.HostKeyUnknownError{Host: "h", Fingerprint: "SHA256:x"}, 9},
		{"host key changed", &knownhosts.HostKeyChangedError{Host: "h", Fingerprint: "SHA256:x"}, 9},
		{"plain", errors.New("something else"), 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := exitCodeFor(c.err); got != c.want {
				t.Errorf("exitCodeFor(%T) = %d, want %d", c.err, got, c.want)
			}
			wrapped := fmt.Errorf("get sessions: %w", c.err)
			if got := exitCodeFor(wrapped); got != c.want {
				t.Errorf("exitCodeFor(wrapped %T) = %d, want %d", c.err, got, c.want)
			}
		})
	}
	if exitCodeFor(nil) != 0 {
		t.Errorf("exitCodeFor(nil) = %d, want 0", exitCodeFor(nil))
	}
}

// TestWaitCodesUnchanged: wait keeps 0 to 5, and an exitError carries its
// own code through the table.
func TestWaitCodesUnchanged(t *testing.T) {
	want := []int{waitSucceeded, waitFailed, waitCrashed, waitTimeout, waitUnknown, waitDaemonError}
	for i, c := range want {
		if c != i {
			t.Errorf("wait code %d = %d, want %d", i, c, i)
		}
		if got := exitCodeFor(fmt.Errorf("wrapped: %w", &exitError{Code: c, Msg: "m"})); got != c {
			t.Errorf("exitCodeFor(exitError{%d}) = %d", c, got)
		}
	}
}

// TestSilenceUsageOnlyForConnectErrors: the RunE wrapper drops the usage
// block for a typed connection error and keeps it for any other error.
func TestSilenceUsageOnlyForConnectErrors(t *testing.T) {
	for _, c := range []struct {
		name      string
		err       error
		wantUsage bool
	}{
		{"plain error", errors.New("bad input"), true},
		{"wrapped unreachable", fmt.Errorf("get: %w", &daemonrpc.UnreachableError{Addr: "a", Err: errors.New("refused")}), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := &cobra.Command{Use: "r"}
			root.AddCommand(&cobra.Command{Use: "leaf", RunE: func(*cobra.Command, []string) error { return c.err }})
			silenceUsageOnConnectErrors(root)
			var out, errOut bytes.Buffer
			root.SetOut(&out)
			root.SetArgs([]string{"leaf"})
			execute(root, &errOut)
			if got := strings.Contains(out.String()+errOut.String(), "Usage:"); got != c.wantUsage {
				t.Errorf("usage printed = %v, want %v:\n%s%s", got, c.wantUsage, out.String(), errOut.String())
			}
			if !strings.Contains(errOut.String(), c.err.Error()) {
				t.Errorf("stderr does not hold the error:\n%s", errOut.String())
			}
		})
	}
}
