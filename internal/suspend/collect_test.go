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
