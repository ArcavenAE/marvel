// Package watcherprobe is the checker and the loggers of the watcher's claude
// probe kit (docs/design/marvel-watcher.md section 6). It measures claude and
// decides nothing: the kit runs on a scratch claude, its loggers append
// monotonic-stamped lines to one log, and Run turns that log into the c1 to
// c13 result that C2 reads.
package watcherprobe

import (
	"errors"
	"io"
)

// The kinds of line in the log.
const (
	KindHook    = "hook"
	KindHookEnv = "hookenv"
	KindStatus  = "status"
	KindMark    = "mark"
)

// The statuses a check can take.
const (
	StatusPass     = "pass"
	StatusFail     = "fail"
	StatusObserved = "observed"
	StatusNotRun   = "not-run"
)

var errNotBuilt = errors.New("watcherprobe: not built")

// Line is one log line.
type Line struct {
	MonoNS int64
	Kind   string
	Name   string
	Body   string
}

// Check is the answer for one of c1 to c13.
type Check struct {
	Status string `json:"status"`
	Value  any    `json:"value,omitempty"`
	Note   string `json:"note,omitempty"`
}

// Result is probe_result.json.
type Result struct {
	BuildStamp string
	Checks     map[string]Check
}

// Format renders l as one physical line.
func Format(Line) string { return "" }

// Parse reads lines written by Format.
func Parse(io.Reader) ([]Line, error) { return nil, errNotBuilt }

// MonoNS reads the system monotonic clock in nanoseconds.
func MonoNS() int64 { return 0 }

// Append adds l to the log at path.
func Append(string, Line) error { return errNotBuilt }

// RecordHook logs one hook firing.
func RecordHook(string, io.Reader, []string) error { return errNotBuilt }

// RecordStatusline logs one statusline refresh and returns the line claude shows.
func RecordStatusline(string, io.Reader) (string, error) { return "", errNotBuilt }

// Run evaluates the log.
func Run([]Line, string) Result { return Result{} }
