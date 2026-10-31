//go:build !darwin

package session

// recordedStart has no comparable form on this platform: the start time codex
// records is not the one childproof reads here, so no pid is ever proven and
// a sweep lists daemon pids without signalling them.
func recordedStart(int64, int64) string { return "" }
