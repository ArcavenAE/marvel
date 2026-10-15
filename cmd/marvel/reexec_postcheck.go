package main

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/arcavenae/marvel/internal/bus"
	"github.com/arcavenae/marvel/internal/daemon"
)

// After the exec the CLI reads what the new daemon says about the hub leaf and
// reports it, instead of claiming that agents keep running (aae-orc-4h9xp,
// section 4). The bound is the supervisor's own cadence: it polls the leaf every
// 30s and trusts a reading for two polls, so 90s is long enough for one fresh
// reading to land and then be read.
const (
	postReexecWait  = 90 * time.Second
	postReexecEvery = 2 * time.Second
)

// reexecObserver is what the check reads and how it waits; tests drive it with
// a fake daemon and a clock that moves only when the check sleeps.
type reexecObserver struct {
	startedAt func() (time.Time, error)  // the daemon's process start, to tell the successor from the old image
	leaf      func() (bus.Status, error) // the supervisor's reading
	now       func() time.Time
	sleep     func(time.Duration)
	wait      time.Duration
	every     time.Duration
}

// liveReexecObserver reads the daemon the CLI is connected to. It works against
// a remote daemon over mrvl://, since it asks only through the same requests.
func liveReexecObserver() reexecObserver {
	return reexecObserver{
		startedAt: func() (time.Time, error) {
			info, err := boundedQuery(versionQueryTimeout, queryDaemonBuild)
			if err != nil {
				return time.Time{}, err
			}
			return info.StartedAt, nil
		},
		leaf: func() (bus.Status, error) {
			var st bus.Status
			resp, err := send(daemon.Request{Method: "bus.status"})
			if err != nil {
				return st, err
			}
			if resp.Error != "" {
				return st, fmt.Errorf("%s", resp.Error)
			}
			return st, json.Unmarshal(resp.Result, &st)
		},
		now: time.Now, sleep: time.Sleep, wait: postReexecWait, every: postReexecEvery,
	}
}

// newReexecObserver builds the observer a command uses. A variable so the
// tests of the commands around it never wait on a real daemon.
var newReexecObserver = liveReexecObserver

// reexecCheckError is the failure of the post-check: the exec happened, and the
// leaf is not up. It is told apart from a failed re-exec so the message says so.
type reexecCheckError struct{ reason string }

func (e *reexecCheckError) Error() string {
	return fmt.Sprintf("hub leaf not up after re-exec (%s)", e.reason)
}

// reexecHandoffOutcome reads the handoff field the daemon puts in its reexec
// response (carried, declined, over-cap, failed or none). An older daemon sends
// none, which reads as empty.
func reexecHandoffOutcome(resp *daemon.Response) string {
	var r struct {
		Handoff string `json:"handoff"`
	}
	if err := json.Unmarshal(resp.Result, &r); err != nil {
		return ""
	}
	return r.Handoff
}

// notCarried is the line a declined handoff gets, from the departing daemon's
// own word: only it knows why nothing travelled.
func notCarried(handoff string) string {
	switch handoff {
	case "declined":
		return "not carried: target cannot read the handoff"
	case "over-cap":
		return "not carried: payload over cap"
	case "failed":
		return "not carried: handoff failed"
	}
	return ""
}

// terminalLeafReasons are causes waiting cannot cure.
var terminalLeafReasons = map[string]bool{
	"dropped on adopt": true, "detached by operator": true, "not enrolled": true,
}

// checkAfterReexec polls until the successor answers and its leaf reading is
// decided or the bound passes. before is the old daemon's start time, so an
// answer from the image that has not exec'd yet is never read as the
// successor's. With no hub it passes and says so; with a hub and no leaf that is
// up on a current reading it returns an error after saying how long it waited,
// why, and the one command that fixes it.
func checkAfterReexec(w io.Writer, before time.Time, handoff string, o reexecObserver) error {
	start := o.now()
	deadline := start.Add(o.wait)
	reason := "the daemon did not answer"
	for {
		if answered, why := o.readOnce(before); answered {
			if why == "" {
				_, _ = fmt.Fprintf(w, "marvel daemon re-executed in place; hub leaf up (waited %s)\n", seconds(o.now().Sub(start)))
				return nil
			}
			if why == "no hub" {
				_, _ = fmt.Fprintln(w, "marvel daemon re-executed in place; no hub is configured, so there is no leaf to check")
				return nil
			}
			reason = why
			if terminalLeafReasons[why] {
				break
			}
		}
		if !o.now().Before(deadline) {
			break
		}
		o.sleep(o.every)
	}
	_, _ = fmt.Fprintf(w, "marvel daemon re-executed in place, but the hub leaf is not up (waited %s): %s\n", seconds(o.now().Sub(start)), reason)
	switch line := notCarried(handoff); {
	case line != "":
		_, _ = fmt.Fprintln(w, line)
	case handoff == "":
		_, _ = fmt.Fprintln(w, "the daemon did not report whether it carried its credentials (an older build)")
	}
	_, _ = fmt.Fprintf(w, "push the leaf credential again: %s\n", repushCommand)
	return &reexecCheckError{reason: reason}
}

// seconds prints a wait in whole seconds, the unit the bound is stated in.
func seconds(d time.Duration) string { return fmt.Sprintf("%ds", int(d.Round(time.Second)/time.Second)) }

// readOnce reads the successor once. answered is false until the daemon answers
// as a new process; why is "" for a leaf that is up on a current reading.
func (o reexecObserver) readOnce(before time.Time) (answered bool, why string) {
	started, err := o.startedAt()
	if err != nil || (!before.IsZero() && started.Equal(before)) {
		return false, ""
	}
	st, err := o.leaf()
	if err != nil {
		return false, ""
	}
	ok, reason := leafRequirement(st, o.now())
	if ok {
		return true, ""
	}
	return true, reason
}
