package api

// MaxHandoffPutBytes caps the text one handoff.put may carry. The tail read
// that finds the marker holds 4 KiB, so 64 KiB is several times what a handoff
// needs; a larger put is refused, never truncated.
const MaxHandoffPutBytes = 64 << 10

// HandoffPutRequest is the handoff.put RPC's wire shape, shared by the CLI
// that sends it and the daemon that consumes it. It names no session: the
// token is the identity, so a caller cannot write a handoff for another seat
// (docs/design/read-only-seat-handoff.md, section 2).
type HandoffPutRequest struct {
	// Token is the session's heartbeat token, from HeartbeatTokenFromEnv.
	Token string `json:"token"`
	// Text is the handoff. marvel appends the role's declared marker when the
	// text does not already end with it.
	Text string `json:"text"`
}

// HandoffPutResult is the daemon's answer to a handoff.put it accepted.
type HandoffPutResult struct {
	// Path is the handoff file marvel wrote.
	Path string `json:"path"`
	// Bytes is the size of the file, marker included.
	Bytes int `json:"bytes"`
}
