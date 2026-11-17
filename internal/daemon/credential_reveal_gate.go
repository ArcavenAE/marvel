package daemon

import (
	"github.com/arcavenae/marvel/internal/config"
)

// errRevealNotEnabled is the refusal credential.reveal answers with until the
// cluster entry turns it on.
const errRevealNotEnabled = "credential.reveal is not enabled: set allow_credential_reveal: true on this cluster's entry in the config and restart the daemon"

// credentialRevealEnabled reads the cluster's allow_credential_reveal switch.
// No cluster entry, or an unset or false switch, is off.
func credentialRevealEnabled(cl *config.Cluster) bool {
	return cl != nil && cl.AllowCredentialReveal
}

// loadCredentialReveal sets the daemon's reveal switch from the cluster entry
// whose socket is ours, found the way loadHostVitals finds it and under the
// same tolerance for a config that loads with a validation error. A config that
// cannot be read at all leaves reveal off.
func (d *Daemon) loadCredentialReveal(socketPath string) {
	on := false
	if cfg, err := config.Load(); configLoadUsable(err) && cfg != nil {
		on = credentialRevealEnabled(cfg.ClusterForSocket(socketPath))
	}
	d.revealAllowed.Store(on)
}
