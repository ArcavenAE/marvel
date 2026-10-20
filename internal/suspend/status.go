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
	return Status{}
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
	return nil
}

// Render writes a status for a person. now is the time the elapsed figure is
// measured to; fromMirror says the daemon did not answer and this was read from
// the mirror.
func Render(st Status, now time.Time, fromMirror bool) string {
	return ""
}

// MirrorPath is where the mirror sits for a daemon listening at socket: beside
// the socket, named after it. A TCP address has no file beside it, so it has no
// mirror and MirrorPath returns "".
func MirrorPath(socket string) string {
	return ""
}

// WriteMirror replaces the mirror with rec. It writes a temporary file beside it
// and renames it, so a reader sees the old mirror or the new one.
func WriteMirror(path string, rec Record) error {
	return nil
}

// ReadMirror reads the mirror. found is false when there is none.
func ReadMirror(path string) (rec Record, found bool, err error) {
	return Record{}, false, nil
}

// RemoveMirror removes the mirror. A mirror that is already gone is not an
// error.
func RemoveMirror(path string) error {
	return nil
}

var (
	_ = json.Marshal
	_ = fmt.Sprintf
	_ = errors.Is
	_ = fs.ErrNotExist
	_ = os.Remove
	_ = filepath.Dir
	_ = strings.Contains
)
