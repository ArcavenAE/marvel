package team

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/session"
	"github.com/arcavenae/marvel/internal/tmux"
)

// The write-rate harness for marvel#1214. It runs the daemon's own reconcile
// pass over N fake seats against a scratch bolt file and a scratch tmux socket
// (TestMain sets both), and counts two things without root: the bbolt write
// transactions the Store starts (Store.BoltUpdates) and the tmux clients the
// driver execs (a shim named tmux, first on PATH, that logs the subcommand and
// runs the real tmux). It never touches a live daemon.
//
// Passes are driven directly, not on the 2 s ticker, so the run is quick and
// repeatable. A per-minute figure is the per-pass count times 30, the passes a
// minute that daemon.ReconcileInterval gives; it is not a wall-clock run.

const (
	writeRateSeats  = 30
	writeRatePasses = 10
)

// countingTmux puts a shim named tmux first on PATH and returns the log it
// writes. Each exec appends one word: the subcommand, with list-panes split
// into list-panes-a (the whole-server form) and list-panes (one target).
// It must run before tmux.NewDriver, which captures the binary path.
func countingTmux(t *testing.T) string {
	t.Helper()
	real, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not available")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "execs.log")
	shim := `#!/bin/sh
sub=
skip=0
for a in "$@"; do
  if [ "$skip" = 1 ]; then skip=0; continue; fi
  case "$a" in
    -u) ;;
    -L) skip=1 ;;
    -*) ;;
    *) sub=$a; break ;;
  esac
done
if [ "$sub" = list-panes ]; then
  for a in "$@"; do
    if [ "$a" = -a ]; then sub=list-panes-a; fi
  done
fi
echo "$sub" >> '` + log + `'
exec '` + real + `' "$@"
`
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// execCounts reads and clears the shim log, returning exec counts by subcommand.
func execCounts(t *testing.T, log string) map[string]int {
	t.Helper()
	counts := map[string]int{}
	raw, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, w := range strings.Fields(string(raw)) {
		counts[w]++
	}
	if err := os.WriteFile(log, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return counts
}

func formatExecs(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	total := 0
	for k, v := range counts {
		keys = append(keys, k)
		total += v
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, counts[k]))
	}
	return fmt.Sprintf("%d (%s)", total, strings.Join(parts, " "))
}

// writeRateRig builds a bolt-backed store, a driver through the shim, and a
// controller holding writeRateSeats running process-alive seats.
func writeRateRig(t *testing.T) (*api.Store, *session.Manager, *Controller, string) {
	t.Helper()
	skipIfNoTmux(t)
	log := countingTmux(t)
	store := api.NewStore()
	if err := store.OpenBolt(filepath.Join(t.TempDir(), "marvel.bolt")); err != nil {
		t.Fatalf("open bolt: %v", err)
	}
	t.Cleanup(func() { _ = store.CloseBolt() })
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	mgr := session.NewManager(store, driver)
	ctrl := NewController(store, mgr)
	ws := "test-write-rate"
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })
	createTeamFixture(t, store, ws, "fleet", []api.Role{{
		Name: "worker", Replicas: writeRateSeats,
		Runtime:       api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"600"}},
		RestartPolicy: api.RestartAlways,
		HealthCheck:   &api.HealthCheck{Type: api.HealthCheckProcessAlive},
	}})
	for i := 0; i < 3; i++ {
		ctrl.ReconcileOnce()
	}
	if got := len(store.ListSessionsByTeamRole(ws, "fleet", "worker")); got != writeRateSeats {
		t.Fatalf("rig has %d seats, want %d", got, writeRateSeats)
	}
	// Let the first panes settle so the measured passes are steady state.
	time.Sleep(200 * time.Millisecond)
	for i := 0; i < 2; i++ {
		ctrl.ReconcileOnce()
	}
	execCounts(t, log)
	return store, mgr, ctrl, log
}

// TestWriteRateByCaller logs the per-pass cost of a steady fleet, split by the
// caller that pays it. It asserts nothing about the numbers; the commits that
// cut them carry their own assertions. Run it with -v to read the figures.
func TestWriteRateByCaller(t *testing.T) {
	if testing.Short() {
		t.Skip("the write-rate harness starts 30 panes and runs about 15 s")
	}
	store, mgr, ctrl, log := writeRateRig(t)

	report := func(label string, fn func()) {
		u0 := store.BoltUpdates()
		for i := 0; i < writeRatePasses; i++ {
			fn()
		}
		u := store.BoltUpdates() - u0
		ex := execCounts(t, log)
		t.Logf("%-34s %d passes: bolt updates %d (%.1f/pass, %.0f/min at 30 passes/min), tmux execs %s",
			label, writeRatePasses, u, float64(u)/float64(writeRatePasses),
			float64(u)/float64(writeRatePasses)*30, formatExecs(ex))
	}
	report("ReconcileOnce (whole pass)", ctrl.ReconcileOnce)
	report("ReapDead only", func() { mgr.ReapDead() })
	report("evaluateHealth only", ctrl.evaluateHealth)
	t.Logf("seats=%d, process-alive health check, no heartbeat roles in this rig", writeRateSeats)
}
