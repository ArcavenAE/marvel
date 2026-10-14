package bus

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/service"
)

// An unstarted supervisor reads as down, with an observation time, and the
// adapter's bus-typed accessor returns exactly what the supervisor reports.
func TestLifecycleStatusReadsTheSupervisor(t *testing.T) {
	s, _, _ := newTestSupervisor(t, "")
	l := NewLifecycle(s)
	before := time.Now()
	st := l.Status()
	if st.State != service.StateDown || st.Ready || st.PID != 0 {
		t.Errorf("unstarted Status = %+v, want down, not ready, no pid", st)
	}
	if st.ObservedAt.Before(before) {
		t.Errorf("ObservedAt %v is before the call at %v", st.ObservedAt, before)
	}
	if got, want := l.BusStatus(), s.Status(); got.URL != want.URL || got.Listen != want.Listen || got.Managed != want.Managed || got.Domain != want.Domain {
		t.Errorf("BusStatus = %+v, want the supervisor's %+v", got, want)
	}
}

// A started broker reads as up with its pid, ready, and the verbs delegate.
func TestLifecycleVerbsDelegateToTheSupervisor(t *testing.T) {
	s, _, _ := newTestSupervisor(t, "")
	l := NewLifecycle(s)
	s.AfterReady = nil
	if err := l.Restart("before start"); err == nil {
		t.Error("Restart before Start returned nil, want the supervisor's refusal")
	} else if want := s.Restart("before start"); want == nil || err.Error() != want.Error() {
		t.Errorf("Restart error %q, want the supervisor's %v", err, want)
	}
	if err := l.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { l.Stop(false) })
	st := l.Status()
	if st.State != service.StateUp || st.PID == 0 || st.PID != s.Status().PID {
		t.Errorf("started Status = %+v, want up with the supervisor's pid %d", st, s.Status().PID)
	}
	if err := l.Reload(); err != nil {
		t.Errorf("Reload: %v", err)
	}
	if err := l.Restart("test"); err != nil {
		t.Errorf("Restart: %v", err)
	}
	if got := l.Status().Restarts; got != s.Status().Restarts {
		t.Errorf("Restarts = %d, want the supervisor's %d", got, s.Status().Restarts)
	}
}

// Stop(false) takes the child down; the status then reads down.
func TestLifecycleStopTakesTheChildDown(t *testing.T) {
	s, _, _ := newTestSupervisor(t, "")
	l := NewLifecycle(s)
	if err := l.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	l.Stop(false)
	if st := l.Status(); st.State != service.StateDown || st.PID != 0 {
		t.Errorf("after Stop Status = %+v, want down with no pid", st)
	}
}

// Only the leaf seed's fingerprint crosses, under its own key.
func TestLifecycleEnvFingerprintReportsOnlyTheLeafSeed(t *testing.T) {
	s, _, _ := newTestSupervisor(t, "")
	l := NewLifecycle(s)
	if fp, ok := l.EnvFingerprint(LeafSeedKey); ok || fp != "" {
		t.Errorf("EnvFingerprint with no seed = %q, %v, want empty and false", fp, ok)
	}
	s.mu.Lock()
	s.leafSeedInEnv, s.leafSeedFP = true, "abc123"
	s.mu.Unlock()
	if fp, ok := l.EnvFingerprint(LeafSeedKey); !ok || fp != "abc123" {
		t.Errorf("EnvFingerprint = %q, %v, want abc123 and true", fp, ok)
	}
	if fp, ok := l.EnvFingerprint("something/else"); ok || fp != "" {
		t.Errorf("EnvFingerprint of another key = %q, %v, want empty and false", fp, ok)
	}
}

// Stop(true) hands the child over: it is still running afterwards.
func TestLifecycleStopWithKeepLeavesTheChildRunning(t *testing.T) {
	s, _, _ := newTestSupervisor(t, "")
	l := NewLifecycle(s)
	if err := l.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := l.Status().PID
	proc, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proc.Kill() })
	l.Stop(true)
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		t.Errorf("pid %d is gone after Stop(keep=true): %v", pid, err)
	}
}

// A broker waiting out a restart delay reads as backoff, not up.
func TestLifecycleStatusReadsBackoff(t *testing.T) {
	s, _, _ := newTestSupervisor(t, "")
	s.mu.Lock()
	s.pid = 4242
	s.backoffTill = time.Now().Add(time.Minute)
	s.mu.Unlock()
	if got := NewLifecycle(s).Status().State; got != service.StateBackoff {
		t.Errorf("State = %q, want backoff", got)
	}
}
