package suspend

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/arcavenae/marvel/internal/childproof"
)

// A fake host: pid to parent, and every pid has an identity unless listed in
// noID.
type fakeHost struct {
	parent map[int]int
	noID   map[int]bool
	server int
	panes  []int
}

func (h *fakeHost) collector() *Collector {
	return &Collector{
		Server: func() (int, error) { return h.server, nil },
		Panes:  func() ([]int, error) { return h.panes, nil },
		Table: func() ([]Row, error) {
			rows := make([]Row, 0, len(h.parent))
			for pid, ppid := range h.parent {
				rows = append(rows, Row{Pid: pid, Ppid: ppid})
			}
			return rows, nil
		},
		Identify: func(pid int) (childproof.Identity, error) {
			if h.noID[pid] {
				return childproof.Identity{}, errors.New("gone")
			}
			return childproof.Identity{Start: fmt.Sprintf("t:%d", pid)}, nil
		},
	}
}

func seatPids(rec Record) []int {
	var out []int
	for _, s := range rec.Seats {
		out = append(out, s.Pid)
	}
	slices.Sort(out)
	return out
}

// The record holds the server, every pane's process and every descendant of a
// pane, each with its identity; the server is never a seat.
func TestCollectTakesTheServerThePanesAndTheirDescendants(t *testing.T) {
	h := &fakeHost{
		server: 100,
		panes:  []int{200, 300},
		parent: map[int]int{100: 1, 200: 100, 201: 200, 202: 201, 300: 100, 400: 1},
	}
	rec, err := h.collector().Collect()
	if err != nil {
		t.Fatal(err)
	}
	if rec.Server.Pid != 100 || rec.Server.Identity.Start != "t:100" {
		t.Errorf("server = %+v", rec.Server)
	}
	if got, want := seatPids(rec), []int{200, 201, 202, 300}; !slices.Equal(got, want) {
		t.Errorf("seats = %v, want %v (400 is no pane's descendant)", got, want)
	}
}

// Freezing the daemon would stop the process that must answer resume, so the
// daemon, its ancestors, what it started (the managed bus) and the invoking
// CLI with its ancestors are left out, even when they sit under a pane.
func TestCollectLeavesOutTheDaemonItsAncestorsItsChildrenAndTheCLI(t *testing.T) {
	h := &fakeHost{
		server: 100,
		panes:  []int{200, 300},
		// 200 is a pane shell; the daemon (210) runs in it and started a broker
		// (211) and a tmux client (212). 300 is a pane shell that runs the CLI
		// (310) under a wrapper (305). 220 is an ordinary seat process.
		parent: map[int]int{100: 1, 200: 100, 210: 200, 211: 210, 212: 210, 220: 200, 300: 100, 305: 300, 310: 305, 320: 300},
	}
	c := h.collector()
	c.Daemon, c.CLI = 210, 310
	rec, err := c.Collect()
	if err != nil {
		t.Fatal(err)
	}
	// Left out: 200 (the daemon's parent), 210, 211, 212, 300 and 305 (the CLI's
	// ancestors), 310. Kept: 220 and 320.
	if got, want := seatPids(rec), []int{220, 320}; !slices.Equal(got, want) {
		t.Errorf("seats = %v, want %v", got, want)
	}
}

// A process that exits between the snapshot and its identity read is dropped,
// not an error.
func TestCollectDropsAProcessThatWentAway(t *testing.T) {
	h := &fakeHost{
		server: 100, panes: []int{200},
		parent: map[int]int{100: 1, 200: 100, 201: 200},
		noID:   map[int]bool{201: true},
	}
	rec, err := h.collector().Collect()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := seatPids(rec), []int{200}; !slices.Equal(got, want) {
		t.Errorf("seats = %v, want %v", got, want)
	}
}

// With the server gone there is nothing to suspend: the error names it.
func TestCollectFailsWhenTheServerCannotBeRead(t *testing.T) {
	c := (&fakeHost{}).collector()
	c.Server = func() (int, error) { return 0, errors.New("no server") }
	if _, err := c.Collect(); err == nil {
		t.Fatal("Collect succeeded with no server")
	}
}

// The re-walk after the server stops returns only what the first walk did not
// hold: a child forked between the snapshot and the stop.
func TestRewalkReturnsOnlyNewProcesses(t *testing.T) {
	h := &fakeHost{
		server: 100, panes: []int{200},
		parent: map[int]int{100: 1, 200: 100, 201: 200},
	}
	c := h.collector()
	rec, err := c.Collect()
	if err != nil {
		t.Fatal(err)
	}
	h.parent[202] = 201 // forked after the snapshot
	got, err := c.Rewalk(rec)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Pid != 202 {
		t.Errorf("Rewalk = %+v, want only pid 202", got)
	}
	again, err := c.Rewalk(Record{Server: rec.Server, Seats: append(rec.Seats, got...)})
	if err != nil || len(again) != 0 {
		t.Errorf("second Rewalk = %+v, %v; want nothing new", again, err)
	}
}

func TestParseTable(t *testing.T) {
	rows, err := ParseTable("  1     0\n 200   100\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[1] != (Row{Pid: 200, Ppid: 100}) {
		t.Errorf("rows = %+v", rows)
	}
	if _, err := ParseTable("12 x\n"); err == nil {
		t.Error("ParseTable accepted a non-numeric ppid")
	}
}

// The server and every pane process must lie outside the daemon's subtree: the
// daemon's children are managed workloads, never seats, so a seat found inside
// means the exclusion would drop a seat that should freeze, or the daemon sits
// where it must not. Suspend refuses and signals nothing.
func TestCollectRefusesWhenAPaneProcessIsInsideTheDaemonsSubtree(t *testing.T) {
	cases := map[string]*fakeHost{
		"pane under the daemon": {
			server: 100, panes: []int{200, 211},
			parent: map[int]int{100: 1, 200: 100, 210: 200, 211: 210},
		},
		"server under the daemon": {
			server: 211, panes: []int{200},
			parent: map[int]int{200: 1, 210: 200, 211: 210},
		},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			c := h.collector()
			c.Daemon = 210
			if _, err := c.Collect(); !errors.Is(err, ErrSeatInDaemonSubtree) {
				t.Fatalf("Collect = %v, want ErrSeatInDaemonSubtree", err)
			}
		})
	}
	// The daemon's own pane shell is its ancestor, not its subtree: that is the
	// ordinary case of a daemon started inside a pane, and it still collects.
	h := &fakeHost{server: 100, panes: []int{200}, parent: map[int]int{100: 1, 200: 100, 210: 200}}
	c := h.collector()
	c.Daemon = 210
	if _, err := c.Collect(); err != nil {
		t.Errorf("a daemon inside a pane refused the suspend: %v", err)
	}
}

// After the server stops, tmux cannot answer: the re-walk reads the process
// table and identities only, from the panes the first walk recorded.
func TestRewalkNeverAsksTmux(t *testing.T) {
	h := &fakeHost{
		server: 100, panes: []int{200},
		parent: map[int]int{100: 1, 200: 100, 201: 200},
	}
	c := h.collector()
	rec, err := c.Collect()
	if err != nil {
		t.Fatal(err)
	}
	asked := 0
	c.Server = func() (int, error) { asked++; return 0, errors.New("tmux is stopped") }
	c.Panes = func() ([]int, error) { asked++; return nil, errors.New("tmux is stopped") }
	h.parent[202] = 201
	got, err := c.Rewalk(rec)
	if err != nil {
		t.Fatalf("Rewalk: %v", err)
	}
	if asked != 0 {
		t.Errorf("Rewalk asked tmux %d time(s), want none", asked)
	}
	if len(got) != 1 || got[0].Pid != 202 {
		t.Errorf("Rewalk = %+v, want pid 202", got)
	}
}

// A Rewalk with no Collect before it has no panes to walk from and says so
// instead of walking from nothing.
func TestRewalkWithoutACollectIsAnError(t *testing.T) {
	c := (&fakeHost{server: 100}).collector()
	if _, err := c.Rewalk(Record{Server: Proc{Pid: 100}}); err == nil {
		t.Error("Rewalk with no earlier Collect succeeded")
	}
}

// A seat that runs suspend from its own pane would keep its process chain
// running while the rest are frozen. Collect refuses before anything is
// signalled when an ancestor of the CLI is the pane pid of a session the daemon
// holds. The set is the daemon's own (the PID of each session in its store); no
// environment variable, flag or identity is read.
func TestCollectRefusesACallerThatIsUnderASeatPane(t *testing.T) {
	// 200 is a seat's pane shell and 300 an operator's own pane of the same
	// server; the daemon (210) runs in pane 400.
	h := &fakeHost{
		server: 100, panes: []int{200, 300, 400},
		parent: map[int]int{
			100: 1, 200: 100, 300: 100, 400: 100,
			201: 200, 202: 201, // a seat's tool shell and the CLI under it
			301: 300, 302: 301, // the operator's shell and the CLI under it
			210: 400,         // the daemon, in its own pane
			900: 1, 901: 900, // a CLI under no pane at all
		},
	}
	cases := map[string]struct {
		cli     int
		wantErr bool
	}{
		"under a seat pane":                {202, true},
		"under a pane that is no session":  {302, false},
		"under no pane":                    {901, false},
		"the daemon's own pane is no seat": {210, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			col := h.collector()
			col.Daemon, col.CLI = 210, c.cli
			col.SeatPids = []int{200}
			_, err := col.Collect()
			if got := errors.Is(err, ErrCallerIsASeat); got != c.wantErr {
				t.Errorf("Collect = %v, ErrCallerIsASeat = %v, want %v", err, got, c.wantErr)
			}
			if !c.wantErr && err != nil {
				t.Errorf("Collect refused a caller that is not a seat: %v", err)
			}
		})
	}
}

// With no seat set the check is off, as for a collector built by a caller that
// holds no sessions; the re-walk never runs it, since the CLI cannot have moved.
func TestCollectWithNoSeatSetDoesNotRefuse(t *testing.T) {
	h := &fakeHost{server: 100, panes: []int{200}, parent: map[int]int{100: 1, 200: 100, 201: 200}}
	col := h.collector()
	col.CLI = 201
	if _, err := col.Collect(); err != nil {
		t.Errorf("Collect = %v with no seat set", err)
	}
}
