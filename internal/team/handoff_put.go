package team

import (
	"errors"
)

// MaxHandoffPutBytes caps the text one handoff.put may carry. The tail read
// that finds the marker holds 4 KiB (maxHandoffTail), so 64 KiB is several
// times what a handoff needs; a larger put is refused, never truncated.
const MaxHandoffPutBytes = 64 << 10

// Refusals of a handoff.put. Each is wrapped with the detail a caller or an
// event needs; callers match with errors.Is.
var (
	// ErrHandoffUnauthorized: the token is absent, unknown, or minted for a
	// record that carries no token hash (a handoff is a write, so it does not
	// inherit the heartbeat's fail-open for unbound records).
	ErrHandoffUnauthorized = errors.New("handoff.put: token does not identify a bound session")
	// ErrHandoffNotRequested: the authenticated session does not hold the
	// role's pending max-age request.
	ErrHandoffNotRequested = errors.New("handoff.put: this session holds no pending handoff request")
	// ErrHandoffNoPath: the role declares no handoff file for marvel to write.
	ErrHandoffNoPath = errors.New("handoff.put: the role declares no handoff file")
	// ErrHandoffTooLarge: the text is empty or over MaxHandoffPutBytes.
	ErrHandoffTooLarge = errors.New("handoff.put: text is empty or too large")
	// ErrHandoffDir: the handoff directory is not the directory marvel
	// recorded when it asked, or it or an ancestor is a link, or the target
	// is not a regular file.
	ErrHandoffDir = errors.New("handoff.put: handoff directory refused")
)

var errHandoffPutNotImplemented = errors.New("handoff.put: not implemented")

// HandoffPut is what a successful handoff.put reports.
type HandoffPut struct {
	// Path is the handoff file marvel wrote.
	Path string
	// Bytes is the size of the file written, marker included.
	Bytes int
}

// PutHandoff writes the handoff of the session that owns token, and nothing
// else. Not implemented yet.
func (c *Controller) PutHandoff(token, text string) (HandoffPut, error) {
	return HandoffPut{}, errHandoffPutNotImplemented
}
