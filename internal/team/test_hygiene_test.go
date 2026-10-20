package team

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// fataler is the part of *testing.T that mustRoleHealth uses, so a test can
// watch it fail without failing itself.
type fataler interface {
	Helper()
	Fatalf(format string, args ...any)
}

// recordingT records Fatalf and stops the calling goroutine the way
// *testing.T does, so code after the call does not run.
type recordingT struct {
	msg   string
	fatal bool
}

func (r *recordingT) Helper() {}

func (r *recordingT) Fatalf(format string, args ...any) {
	r.fatal = true
	r.msg = format
	runtime.Goexit()
}

// mustRoleHealth returns the snapshot for a role and fails the test when none
// was recorded. A bare `rh, _ := ctrl.RoleHealthSnapshot(...)` hands a nil to
// the next line when setup failed, and one failed setup then panics the whole
// test binary and skips every test after it.
func mustRoleHealth(t fataler, ctrl *Controller, workspace, team, role string) *RoleHealth {
	_ = t
	rh, _ := ctrl.RoleHealthSnapshot(workspace, team, role)
	return rh
}

func TestMustRoleHealthFailsTheTestWhenNothingWasRecorded(t *testing.T) {
	ctrl := NewController(api.NewStore(), nil)
	rec := &recordingT{}
	done := make(chan *RoleHealth, 1)
	go func() {
		defer func() { _ = recover() }()
		done <- mustRoleHealth(rec, ctrl, "ws", "squad", "worker")
	}()
	rh := <-done
	if !rec.fatal {
		t.Fatalf("mustRoleHealth returned %v without failing the test", rh)
	}
	if !strings.Contains(rec.msg, "no health") {
		t.Errorf("failure message %q does not say no health was recorded", rec.msg)
	}
}

// psProblem says why tmux-backed tests cannot run, or "" when they can. The
// tmux driver reads `ps` to tell an absent server from an unreachable one and
// refuses to create a session when it cannot, so where ps is blocked (a
// sandbox-exec profile does that) every tmux-backed test fails with a bare
// has-session error. A skip that names ps is the better answer.
func psProblem(probe func() error) string {
	_ = probe
	return ""
}

func TestPsProblemNamesTheMissingPs(t *testing.T) {
	if got := psProblem(func() error { return nil }); got != "" {
		t.Errorf("psProblem with a working ps = %q, want empty", got)
	}
	got := psProblem(func() error { return errors.New("operation not permitted") })
	for _, want := range []string{"ps", "operation not permitted", "tmux"} {
		if !strings.Contains(got, want) {
			t.Errorf("psProblem with a blocked ps = %q, want it to mention %q", got, want)
		}
	}
}
