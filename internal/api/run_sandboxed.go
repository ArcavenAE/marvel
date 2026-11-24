package api

// MaxRunOutputBytes caps each of stdout and stderr in a RunSandboxedResult.
// Whole-at-exit with a cap is the first-build default; whether the call
// streams is an open ruling (marvel#1596, open question 2).
const MaxRunOutputBytes = 1 << 20

// RunSandboxedRequest is the run.sandboxed RPC's wire shape. It names no
// session or role: the token is the identity, and the daemon reads the role
// from its own record (docs/design/seat-tcc-deny.md, section 14). The shape is
// provisional until the stdin, tty and streaming rulings arrive.
type RunSandboxedRequest struct {
	// Token is the session's heartbeat token, from HeartbeatTokenFromEnv.
	Token string `json:"token"`
	// Argv is the command, never a shell string.
	Argv []string `json:"argv"`
	// Dir is the working directory.
	Dir string `json:"dir,omitempty"`
	// Env is the environment of the child, KEY=VALUE entries.
	Env []string `json:"env,omitempty"`
	// Profile is the run profile text. The daemon composes it after the role's
	// own profile and before the deny block; it is never applied alone.
	Profile string `json:"profile,omitempty"`
	// TimeoutSeconds bounds the run; the daemon kills the child at the limit.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
	// Stdin is fed to the child and then closed.
	Stdin []byte `json:"stdin,omitempty"`
}

// RunSandboxedResult is the daemon's answer to a run it carried out.
type RunSandboxedResult struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	// Truncated is true when either stream hit MaxRunOutputBytes.
	Truncated bool `json:"truncated,omitempty"`
	// TimedOut is true when the daemon killed the child at the time limit.
	TimedOut bool `json:"timed_out,omitempty"`
}
