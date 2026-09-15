package api

import (
	"crypto/rand"
	"fmt"
)

// NewHarnessSessionID mints a fresh RFC 4122 version 4 UUID naming one
// harness launch.
//
// This is the spawn-time half of "marvel is entitled to assign identity,
// not discover it" (aae-orc-ca7y). marvel already constructs the process
// environment and the command line at spawn, so a harness that accepts a
// caller-chosen session id costs nothing to name, and naming it removes
// the binding problem instead of pricing it: the alternative on the table
// was a per-pid lsof probe that cannot find a Claude Code transcript at
// all, because the harness opens the file, appends, and closes rather than
// holding the descriptor.
//
// Minted per LAUNCH, never held per session record. Measured against
// Claude Code 2.1.271 on 2026-09-15:
//
//	--session-id <fresh uuid>  exit 0, transcript written to
//	                           ~/.claude/projects/<cwd slug>/<uuid>.jsonl
//	--session-id <reused uuid> exit 1, "Session ID <uuid> is already in use."
//	--session-id not-a-uuid    exit 1, "Invalid session ID. Must be a valid UUID."
//
// So a value held stable across a session's life would fail every respawn
// after the first, and the UUID shape is enforced rather than advisory.
// Both facts are the reason this is a function and not a formatting of the
// session key.
func NewHarnessSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("mint harness session id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
