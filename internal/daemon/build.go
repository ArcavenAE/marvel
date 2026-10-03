package daemon

import "time"

// MethodVersion is the read-only RPC that reports the running daemon's build.
const MethodVersion = "version"

// Build names a marvel build.
type Build struct {
	Version string `json:"version"`
	Channel string `json:"channel"`
	Commit  string `json:"commit,omitempty"`
}

// BuildInfo is what a running daemon reports about itself.
type BuildInfo struct {
	Build
	StartedAt time.Time `json:"started_at"`
	PID       int       `json:"pid"`
}

// handleVersion answers MethodVersion. Scaffold.
func (d *Daemon) handleVersion() Response {
	return Response{}
}
