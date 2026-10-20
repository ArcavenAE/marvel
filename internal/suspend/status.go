package suspend

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StateRunning is what status reports when there is no record.
const StateRunning = "running"

// Status is the answer to suspend.status, and what a client reads from the
// mirror when the daemon does not answer (design section 4). It is built from
// the record alone: no lock, no tmux.
type Status struct {
	// State is running, or the record's state.
	State string `json:"state"`
	// Record is nil when the cluster is running.
	Record *Record `json:"record,omitempty"`
}

// StatusOf is the status a record gives. A nil record is a running cluster.
func StatusOf(rec *Record) Status {
	if rec == nil {
		return Status{State: StateRunning}
	}
	return Status{State: string(rec.State), Record: rec}
}

// ExitCode is marvel status's exit code for a status: 0 running, 3 suspended
// or partway (suspending, resuming).
func (s Status) ExitCode() int {
	if s.State == StateRunning {
		return 0
	}
	return 3
}

// Recipe is the manual resume, for the operator to run by hand when marvel
// cannot (design section 6): the server, then each seat, each with the start
// time and process group it was recorded with, so a pid that now names another
// process can be told apart before it is signalled.
func Recipe(rec Record) []string {
	line := func(p Proc, what string) string {
		return fmt.Sprintf("kill -CONT %d   # %s, started %s, pgid %d", p.Pid, what, p.Identity.Start, p.Identity.Pgid)
	}
	out := []string{line(rec.Server, "tmux server")}
	for _, s := range rec.Seats {
		out = append(out, line(s, "seat"))
	}
	return out
}

// Render writes a status for a person. now is the time the elapsed figure is
// measured to; fromMirror says the daemon did not answer and this was read from
// the mirror.
func Render(st Status, now time.Time, fromMirror bool) string {
	var b strings.Builder
	if fromMirror {
		fmt.Fprintf(&b, "daemon not answering; read from the suspend mirror beside its socket\n")
	}
	if st.Record == nil {
		fmt.Fprintf(&b, "cluster: %s\n", st.State)
		return b.String()
	}
	r := st.Record
	fmt.Fprintf(&b, "cluster: %s since %s (%s ago) by %s via %s\n",
		st.State, r.Since.UTC().Format(time.RFC3339), now.Sub(r.Since).Round(time.Second), r.Actor, r.Source)
	fmt.Fprintf(&b, "seats: %d recorded at stop time, plus the tmux server\n", len(r.Seats))
	fmt.Fprintf(&b, "bus presence lapses while suspended: a presence reader shows every seat as gone until resume\n")
	fmt.Fprintf(&b, "if marvel cannot run, the operator resumes by hand (never a seat or a remote key):\n")
	for _, l := range Recipe(*r) {
		fmt.Fprintf(&b, "  %s\n", l)
	}
	return b.String()
}

// MirrorPath is where the mirror sits for a daemon listening at socket: beside
// the socket, named after it. A TCP address has no file beside it, so it has no
// mirror and MirrorPath returns "".
func MirrorPath(socket string) string {
	if socket == "" || strings.Contains(socket, ":") {
		return ""
	}
	return socket + ".suspend.json"
}

// WriteMirror replaces the mirror with rec. It writes a temporary file beside it
// and renames it, so a reader sees the old mirror or the new one.
func WriteMirror(path string, rec Record) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("encode suspend mirror: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".suspend-*.json")
	if err != nil {
		return fmt.Errorf("write suspend mirror: %w", err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write suspend mirror: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write suspend mirror: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write suspend mirror: %w", err)
	}
	return nil
}

// ReadMirror reads the mirror. found is false when there is none.
func ReadMirror(path string) (rec Record, found bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("read suspend mirror: %w", err)
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		return Record{}, false, fmt.Errorf("parse suspend mirror %s: %w", path, err)
	}
	return rec, true, nil
}

// RemoveMirror removes the mirror. A mirror that is already gone is not an
// error.
func RemoveMirror(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove suspend mirror: %w", err)
	}
	return nil
}
