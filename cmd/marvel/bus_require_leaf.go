package main

import (
	"fmt"
	"time"

	"github.com/arcavenae/marvel/internal/bus"
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
