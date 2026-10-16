package bus

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/arcavenae/marvel/internal/backoff"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/events"
)

// A broker that crash-loops waits as long as a role does: the supervisor's
// default schedule is the shared one.
func TestNewSupervisorRestartsOnTheSharedSchedule(t *testing.T) {
	if _, err := exec.LookPath("nats-server"); err != nil {
		t.Skip("nats-server not on PATH")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "state", "nats")
	listen := "127.0.0.1:" + strconv.Itoa(freePort(t))
	rb := config.ResolvedBus{Managed: true, Listen: listen, URL: "nats://" + listen, StoreDir: dir}
	m, err := NewManager(dir, "t"+strconv.Itoa(freePort(t)), rb, &teams{}, func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewSupervisor(m, filepath.Join(root, "run"), filepath.Join(root, "log"), events.NewRing(8))
	if err != nil {
		t.Fatal(err)
	}
	attach(t, s, newProcTable(t))
	for _, n := range []int{1, 2, 3, 6, 60} {
		if got, want := s.backoff(n), backoff.Restart.Wait(n); got != want {
			t.Errorf("backoff(%d) = %s, want %s", n, got, want)
		}
	}
}
