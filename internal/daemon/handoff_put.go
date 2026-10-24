package daemon

import (
	"encoding/json"
	"fmt"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// handleHandoffPut writes the handoff of the session that owns the presented
// token, into the file its role declares, when that session holds the role's
// pending max-age request. The request names no session, and the gate is the
// controller's (team.Controller.PutHandoff). Neither the token nor the text is
// logged.
func (d *Daemon) handleHandoffPut(params json.RawMessage) daemonrpc.Response {
	var p api.HandoffPutRequest
	if err := json.Unmarshal(params, &p); err != nil {
		return daemonrpc.Response{Error: fmt.Sprintf("bad params: %v", err)}
	}
	got, err := d.teamCtrl.PutHandoff(p.Token, p.Text)
	if err != nil {
		return daemonrpc.Response{Error: err.Error()}
	}
	return respond(api.HandoffPutResult{Path: got.Path, Bytes: got.Bytes})
}
