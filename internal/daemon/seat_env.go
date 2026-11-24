package daemon

import (
	"log"
	"strings"

	"github.com/arcavenae/marvel/internal/config"
)

// loadSeatEnv hands the driver the seat_env of the cluster entry whose socket is
// ours, found the way loadAccountWarn finds it. An unset block changes nothing. A
// block that does not check out is logged and reads as unset, so a typo in
// seat_env costs the change and never the daemon.
func (d *Daemon) loadSeatEnv(socketPath string) {
	cfg, err := config.Load()
	if !configLoadUsable(err) || cfg == nil {
		return
	}
	cl := cfg.ClusterForSocket(socketPath)
	spec, err := cl.SeatEnvSpec()
	if err != nil {
		log.Printf("seat env: %v; the seats start with the daemon's environment as it is", err)
		return
	}
	if len(spec.PathPrepend) == 0 && len(spec.Unset) == 0 && len(spec.Secrets) == 0 {
		return
	}
	d.driver.SetSeatEnv(spec.PathPrepend, spec.Unset)
	log.Printf("seat env: cluster %s puts %d directories in front of PATH and removes %d more variables in every seat", cl.Name, len(spec.PathPrepend), len(spec.Unset))
	if vars := spec.SecretVars(); len(vars) > 0 {
		// Names only, in the log and on the tmux argument list. Opt-in: a cluster
		// that declares no secrets keeps every seat's environment as it was.
		d.driver.SetSeatSecretNames(vars)
		if err := d.driver.StripServerEnv(); err != nil {
			log.Printf("seat env: %v", err)
		}
		log.Printf("seat env: cluster %s keeps %d seat secret variable(s) out of the tmux server and every pane: %s", cl.Name, len(vars), strings.Join(vars, ", "))
	}
}
