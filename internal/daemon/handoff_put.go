package daemon

import (
	"encoding/json"

	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// handleHandoffPut writes the handoff of the session that owns the presented
// token. Not implemented yet.
func (d *Daemon) handleHandoffPut(params json.RawMessage) daemonrpc.Response {
	return daemonrpc.Response{Error: "handoff.put: not implemented"}
}
