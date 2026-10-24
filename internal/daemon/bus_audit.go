package daemon

import (
	"encoding/json"

	"github.com/arcavenae/marvel/internal/bus"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// handleBusAudit stub.
func (d *Daemon) handleBusAudit(_ json.RawMessage) daemonrpc.Response {
	return daemonrpc.Response{Error: "not implemented"}
}

// auditMissText stub.
func auditMissText(_ *bus.AuditMiss) string { return "" }
