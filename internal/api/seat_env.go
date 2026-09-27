package api

import (
	"os"
	"strings"
)

// InheritedSessionEnv lists the variables a Claude Code session exports to
// its children that no seat may inherit (aae-orc-31mlk, aae-orc#418). A
// daemon started from such a session carries them, and a tmux server started
// by that daemon hands them to every pane, so any harness, Claude or not,
// could act on the parent session's messaging channel.
//
// Stripped at three layers: the daemon unsets them at start, the tmux driver
// execs tmux without them, and every pane command is prefixed with `env -u`
// for each (a tmux server started earlier by a dirty process still holds
// them in its global environment).
//
// Deliberately NOT here, and TestInheritedSessionEnvKeepsBackendSelectors
// holds the line: every CLAUDE_CODE_USE_* backend selector (the backend
// overlay pins them, backend_overlay.go) and the config-shaped names
// (CLAUDE_EFFORT, CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION,
// CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS, CLAUDE_CODE_AUTO_COMPACT_WINDOW),
// which belong to per-role env (marvel#311). A CLAUDE_CODE_* prefix strip
// would silently unpin backend selection. An allowlist is the stronger end
// state and waits on #311.
//
// CLAUDE_CODE_AUTO_COMPACT_WINDOW is kept knowingly, not overlooked. It is a
// setting, not a credential or an identity: it grants nothing and names no
// session. Stripping it one name at a time would also strip it where an
// operator exported it on purpose for every seat, and marvel has no per-role
// declaration yet to put it back. The cost of keeping it is real: a daemon
// started from a session with a non-default compaction window passes that
// window to every claude seat, which moves the point where the harness
// compacts relative to the context limit marvel resolves and a shift trigger
// arms against. Until #311 lands, set it in runtime.env per role, or start
// the daemon without it.
var InheritedSessionEnv = []string{
	// Credential and capability address: bearer access to the parent
	// session's messaging channel.
	"CLAUDE_CODE_MESSAGING_TOKEN",
	"CLAUDE_CODE_MESSAGING_SOCKET",
	// Parent-session identity: a seat is its own session, never a child
	// of the process that happened to start the daemon.
	"CLAUDE_CODE_SESSION_ID",
	"CLAUDE_CODE_CHILD_SESSION",
	"CLAUDE_CODE_SESSION_ATTENDED",
	"CLAUDE_CODE_ENTRYPOINT",
	"CLAUDE_CODE_EXECPATH",
	"CLAUDECODE",
	"CLAUDE_PID",
}

// IsInheritedSessionEnv reports whether name is on the denylist.
func IsInheritedSessionEnv(name string) bool {
	for _, n := range InheritedSessionEnv {
		if n == name {
			return true
		}
	}
	return false
}

// ScrubInheritedSessionEnv returns env (KEY=VALUE pairs, as os.Environ
// gives) without the denylisted names. The input is not modified.
func ScrubInheritedSessionEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if IsInheritedSessionEnv(k) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// UnsetInheritedSessionEnv removes the denylisted names from this process's
// environment and returns the names it removed, never their values.
func UnsetInheritedSessionEnv() ([]string, error) {
	var removed []string
	for _, n := range InheritedSessionEnv {
		if _, ok := os.LookupEnv(n); !ok {
			continue
		}
		if err := os.Unsetenv(n); err != nil {
			return removed, err
		}
		removed = append(removed, n)
	}
	return removed, nil
}
