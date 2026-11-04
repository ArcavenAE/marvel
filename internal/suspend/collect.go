package suspend

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/arcavenae/marvel/internal/childproof"
)

// Row is one line of a process-table snapshot.
type Row struct{ Pid, Ppid int }

// Collector builds the pid list a suspend records, while tmux still answers
// (docs/design/fleet-suspend-resume.md section 2). It takes one snapshot of the
// process table, so it carries no stability check: a process forked between the
// snapshot and the stop is caught only by Rewalk, and one forked after Rewalk
// keeps running while its seat is stopped. A later change adds the
// two-matching-snapshots walk.
//
// Every field is a seam: the daemon wires the tmux driver and ps, a test wires
// fakes.
type Collector struct {
	// Server returns the tmux server's pid; Panes returns the pid of every
	// pane's process.
	Server func() (int, error)
	Panes  func() ([]int, error)
	// Table returns a process-table snapshot.
	Table func() ([]Row, error)
	// Identify reads a pid's live identity; an error means the process is gone.
	Identify func(pid int) (childproof.Identity, error)
	// Daemon is the daemon's pid and CLI the invoking CLI's. The daemon, its
	// ancestors and everything it started (the managed bus among them), and the
	// CLI and its ancestors, are never recorded: stopping the daemon would stop
	// the process that must answer resume. Zero means none.
	Daemon, CLI int

	// roots are the pane processes Collect found. Rewalk starts from them,
	// because tmux cannot answer once its server is stopped.
	roots []int
}

// ErrNoServer is returned when the tmux server pid cannot be read.
var ErrNoServer = errors.New("suspend: the tmux server pid cannot be read")

// ErrSeatInDaemonSubtree is returned when the tmux server or a pane process
// lies inside the daemon's own subtree. The daemon's children are managed
// workloads, which have no pane, so a seat found there means the exclusion is
// wrong for this host; the suspend refuses before it signals anything.
var ErrSeatInDaemonSubtree = errors.New("suspend: the tmux server or a pane process is inside the daemon's own process subtree, so the exclusion cannot be applied safely")

// Collect returns a Record with Server and Seats set and every other field
// zero: the caller adds the actor, source and time and hands it to Freeze. It
// is the only place tmux is asked, so it runs before the server stops.
func (c *Collector) Collect() (Record, error) {
	serverPid, err := c.Server()
	if err != nil {
		return Record{}, fmt.Errorf("%w: %v", ErrNoServer, err)
	}
	panes, err := c.Panes()
	if err != nil {
		return Record{}, fmt.Errorf("suspend: list the pane processes: %w", err)
	}
	server, seats, err := c.walk(serverPid, panes)
	if err != nil {
		return Record{}, err
	}
	c.roots = panes
	return Record{Server: server, Seats: seats}, nil
}

// Rewalk walks again, after the server has stopped, and returns the processes
// rec does not hold: a child forked between the first snapshot and the stop.
// It reads the process table and identities only, from the panes Collect
// recorded; tmux cannot answer now.
func (c *Collector) Rewalk(rec Record) ([]Proc, error) {
	if c.roots == nil {
		return nil, errors.New("suspend: re-walk before a collect: there are no panes to walk from")
	}
	_, seats, err := c.walk(rec.Server.Pid, c.roots)
	if err != nil {
		return nil, err
	}
	held := map[int]bool{rec.Server.Pid: true}
	for _, s := range rec.Seats {
		held[s.Pid] = true
	}
	var fresh []Proc
	for _, s := range seats {
		if !held[s.Pid] {
			fresh = append(fresh, s)
		}
	}
	return fresh, nil
}

// walk reads the table and returns the server and every pane process with its
// descendants, minus the exclusions.
func (c *Collector) walk(serverPid int, panes []int) (Proc, []Proc, error) {
	rows, err := c.Table()
	if err != nil {
		return Proc{}, nil, fmt.Errorf("suspend: read the process table: %w", err)
	}
	parent := make(map[int]int, len(rows))
	kids := make(map[int][]int, len(rows))
	for _, r := range rows {
		parent[r.Pid] = r.Ppid
		kids[r.Ppid] = append(kids[r.Ppid], r.Pid)
	}

	skip := map[int]bool{serverPid: true}
	ancestors := func(pid int) {
		for seen := 0; pid > 1 && seen < len(rows)+1; seen++ {
			skip[pid] = true
			pid = parent[pid]
		}
	}
	// The daemon's subtree: what it started, the managed workloads. Kept apart
	// from the ancestors, which are ordinary pane shells.
	subtree := map[int]bool{}
	if c.Daemon > 1 {
		ancestors(c.Daemon)
		stack := []int{c.Daemon}
		for len(stack) > 0 {
			p := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if subtree[p] {
				continue
			}
			subtree[p] = true
			skip[p] = true
			stack = append(stack, kids[p]...)
		}
	}
	if c.CLI > 1 {
		ancestors(c.CLI)
	}
	// Fail closed: the daemon's children have no pane, so none may be the tmux
	// server or a pane process.
	if subtree[serverPid] {
		return Proc{}, nil, ErrSeatInDaemonSubtree
	}
	for _, p := range panes {
		if subtree[p] {
			return Proc{}, nil, ErrSeatInDaemonSubtree
		}
	}

	// An excluded process is not recorded, but its children still are: the
	// pane shell the daemon runs in is left out and the seats beside it are not.
	seen := map[int]bool{}
	var order []int
	var visit func(pid int)
	visit = func(pid int) {
		if seen[pid] {
			return
		}
		seen[pid] = true
		if !skip[pid] {
			order = append(order, pid)
		}
		for _, k := range kids[pid] {
			visit(k)
		}
	}
	for _, p := range panes {
		visit(p)
	}

	id, err := c.Identify(serverPid)
	if err != nil {
		return Proc{}, nil, fmt.Errorf("%w: %v", ErrNoServer, err)
	}
	server := Proc{Pid: serverPid, Identity: id}
	seats := make([]Proc, 0, len(order))
	for _, pid := range order {
		id, err := c.Identify(pid)
		if err != nil {
			continue // exited since the snapshot
		}
		seats = append(seats, Proc{Pid: pid, Identity: id})
	}
	return server, seats, nil
}

// ParseTable parses `ps -axo pid=,ppid=` output.
func ParseTable(out string) ([]Row, error) {
	var rows []Row
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) != 2 {
			return nil, fmt.Errorf("unreadable process-table line %q", line)
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("unreadable process-table line %q", line)
		}
		rows = append(rows, Row{Pid: pid, Ppid: ppid})
	}
	return rows, nil
}

// PSTable reads the process table with ps.
func PSTable() ([]Row, error) {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	return ParseTable(string(out))
}
