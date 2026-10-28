package daemon

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// handlePrunePlan answers marvel plan -f <manifest> --prune: what marvel work
// --prune would delete for the manifest's source, and why every other team is
// kept. It takes apply's params and writes nothing: no store write, no event,
// no tmux (docs/design/work-prune.md section 4). It parses the manifest as
// apply does and runs none of apply's other pre-flights, which the full
// manifest plan will (aae-orc-qawkf).
func (d *Daemon) handlePrunePlan(params json.RawMessage) daemonrpc.Response {
	var p applyParams
	if err := json.Unmarshal(params, &p); err != nil {
		return daemonrpc.Response{Error: fmt.Sprintf("bad params: %v", err)}
	}
	if strings.TrimSpace(p.ManifestSource) == "" {
		return daemonrpc.Response{Error: "prune_plan needs a manifest source; nothing can be pruned without one"}
	}
	m, err := api.ParseManifestBytes(p.ManifestData)
	if err != nil {
		return daemonrpc.Response{Error: err.Error()}
	}
	return respond(api.PlanPrune(m, p.ManifestSource, d.store))
}
