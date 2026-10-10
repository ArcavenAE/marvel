package daemon

import (
	"context"
	"encoding/json"

	"github.com/arcavenae/marvel/internal/broker"
)

// UseBrokerDriver turns the credential broker on with d as its driver. A
// daemon that never calls it answers broker.token with a refusal and issues
// nothing.
func (d *Daemon) UseBrokerDriver(drv broker.Driver) {}

func (d *Daemon) handleBrokerToken(ctx context.Context, params json.RawMessage) Response {
	return Response{}
}
