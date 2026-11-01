package main

import (
	"bytes"
	"errors"
	"testing"
)

func TestGooseRefuseWritesReasonAndExits78(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ arg, want string }{
		{"approve-headless", "marvel: goose launch refused: approve-headless\n"},
		{"auto-unsandboxed", "marvel: goose launch refused: auto-unsandboxed\n"},
		{"Not A Slug; rm -rf", "marvel: goose launch refused: unreadable-reason\n"},
	} {
		cmd := newGooseRefuseCmd()
		var out bytes.Buffer
		cmd.SetErr(&out)
		cmd.SetArgs([]string{tc.arg})
		err := cmd.Execute()
		if got := exitCodeFor(err); got != 78 {
			t.Errorf("%q: exit %d, want 78 (err %v)", tc.arg, got, err)
		}
		if out.String() != tc.want {
			t.Errorf("%q: stderr %q, want %q", tc.arg, out.String(), tc.want)
		}
	}
}

func TestGooseRefuseIsHiddenAndTakesOneArgument(t *testing.T) {
	t.Parallel()
	cmd := newGooseRefuseCmd()
	if !cmd.Hidden {
		t.Error("goose-refuse is internal and must be hidden")
	}
	cmd.SetArgs(nil)
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err == nil || errors.As(err, new(*exitError)) {
		t.Errorf("no argument: err %v, want a usage error and not the refusal exit", err)
	}
}
