package main

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/arcavenae/marvel/internal/bus"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// repushCommand is the one command that puts a lost leaf credential back. The
// seed source stays a placeholder: marvel never prints a seed or where an
// operator keeps one.
const repushCommand = "marvel credential put bus/leaf --value-file <seed-file>"

// leafRequirement answers `marvel bus status --require-leaf`: ok only when a
// hub is configured, the leaf is attached, and the leaf is up on a reading that
// is still valid at now. Otherwise it returns the reason, in the words a
// script's log should carry. A reading past its LeafValidUntil is unknown, not
// up (the supervisor polls every 30s and trusts a reading for two polls).
func leafRequirement(st bus.Status, now time.Time) (bool, string) {
	switch {
	case !st.Managed || st.Leaf == "n/a":
		return false, "no hub"
	case st.LeafSource == "dropped-on-adopt":
		return false, "dropped on adopt"
	case st.LeafSource == "unknown":
		return false, "unknown"
	case st.Leaf == "detached":
		return false, "detached by operator"
	case st.Leaf == "unenrolled":
		return false, "not enrolled"
	case st.Leaf == "down":
		return false, "down"
	case st.Leaf != "up":
		return false, "unknown"
	}
	until, err := time.Parse(time.RFC3339, st.LeafValidUntil)
	if err != nil || !now.Before(until) {
		return false, "unknown"
	}
	return true, ""
}

// busStatusResult is what `marvel bus status` does with the daemon's answer: it
// prints the status, and with requireLeaf it fails unless leafRequirement holds.
func busStatusResult(w io.Writer, resp *daemonrpc.Response, requireLeaf bool, now time.Time) error {
	if resp.Error != "" {
		if requireLeaf && resp.Error == daemonrpc.NoBusReply {
			return requireLeafError("no hub")
		}
		return fmt.Errorf("%s", resp.Error)
	}
	var st bus.Status
	if err := json.Unmarshal(resp.Result, &st); err != nil {
		return fmt.Errorf("decode bus status: %w", err)
	}
	printBusStatus(w, st)
	if requireLeaf {
		if ok, why := leafRequirement(st, now); !ok {
			return requireLeafError(why)
		}
	}
	return nil
}

// leafSourceRows renders the leaf source line of `bus status` for a source
// that is not the ordinary one, so the common case stays quiet.
func leafSourceRows(st bus.Status) string {
	switch st.LeafSource {
	case "dropped-on-adopt":
		return "leaf-src: dropped-on-adopt (the broker was started with a leaf seed this daemon no longer holds; push it again: " + repushCommand + ")\n"
	case "unknown":
		return "warning:  the broker's sidecar predates the leaf record, so whether it holds a leaf seed is not known\nleaf-src: unknown\n"
	}
	return ""
}

// requireLeafError is the error `bus status --require-leaf` exits with.
func requireLeafError(why string) error {
	return fmt.Errorf("--require-leaf: the hub leaf is not up (%s)", why)
}

// stopKeepBusNotice says what `marvel stop --keep-bus` costs: a detach keeps
// the broker but drops the daemon's in-memory credentials, so a leafed cluster
// is off the hub until the seed is pushed again. held is the daemon's own count;
// nil means an older daemon that does not report one. It never refuses the stop:
// stop --keep-bus is the documented recovery path.
func stopKeepBusNotice(held *int) string {
	switch {
	case held == nil:
		return "note: any transient credentials the daemon holds will be dropped (they live in memory only); after the next daemon starts, push the leaf credential again: " + repushCommand
	case *held == 0:
		return ""
	}
	return fmt.Sprintf("note: %d transient credential(s) will be dropped (they live in memory only); after the next daemon starts, push the leaf credential again: %s", *held, repushCommand)
}
