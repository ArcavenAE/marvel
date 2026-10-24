package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/arcavenae/marvel/internal/bus"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// busAuditParams names the record `marvel bus audit` reads.
type busAuditParams struct {
	Seq uint64 `json:"seq"`
}

// handleBusAudit serves `marvel bus audit`: one record of the managed broker's
// AGENT_AUDIT stream, by number, read as the broker's admin identity. It
// returns the sequence, time, subject, four headers and the body, nothing
// else. It publishes nothing and creates no consumer.
func (d *Daemon) handleBusAudit(params json.RawMessage) daemonrpc.Response {
	if d.bus == nil {
		return daemonrpc.Response{Error: "no managed bus is configured for this cluster; marvel reads the audit log only where it runs the broker"}
	}
	var p busAuditParams
	if err := json.Unmarshal(params, &p); err != nil {
		return daemonrpc.Response{Error: fmt.Sprintf("bad params: %v", err)}
	}
	if p.Seq == 0 {
		return daemonrpc.Response{Error: "seq must be a whole number of 1 or more"}
	}
	admin := d.bus.Admin()
	rec, err := bus.ReadAuditRecord(context.Background(), d.bus.URL(), admin.Name, admin.Password, p.Seq)
	var miss *bus.AuditMiss
	switch {
	case errors.As(err, &miss):
		return daemonrpc.Response{Error: auditMissText(miss)}
	case err != nil:
		return daemonrpc.Response{Error: fmt.Sprintf("read %s: %v", bus.AuditStream, err)}
	}
	return respond(rec)
}

// auditMissText is the text for each way a number can have no record.
func auditMissText(m *bus.AuditMiss) string {
	switch m.Kind {
	case bus.AuditNoStream:
		return "this broker has no " + bus.AuditStream + " stream"
	case bus.AuditEmpty:
		return bus.AuditStream + " holds no records"
	case bus.AuditAgedOut:
		return fmt.Sprintf("seq %d has aged out; %s holds %d to %d", m.Seq, bus.AuditStream, m.First, m.Last)
	case bus.AuditPastEnd:
		return fmt.Sprintf("seq %d is past the last record, %d; check the number came from this cluster's audit line", m.Seq, m.Last)
	case bus.AuditGap:
		return fmt.Sprintf("seq %d was deleted or purged; %s holds %d to %d with gaps", m.Seq, bus.AuditStream, m.First, m.Last)
	default:
		return fmt.Sprintf("seq %d has no record", m.Seq)
	}
}
