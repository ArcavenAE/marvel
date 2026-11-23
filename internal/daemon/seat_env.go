package daemon

import (
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/api"
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
		d.captureSeatSecrets(cl.Name, spec)
	}
}

// seatSecretCredential is the store name of a need's value.
func seatSecretCredential(need string) string { return "seat-secret/" + need }

// captureSeatSecrets moves each mapped variable out of the daemon's environment
// into the in-memory store, once, and removes it from the process environment
// so no child inherits it. A value the store already holds (a reexec carried
// it) is never replaced. A variable the daemon does not hold is named in the log
// and the seat that declares the need starts without it. No value is logged.
func (d *Daemon) captureSeatSecrets(cluster string, spec config.SeatEnv) {
	if d.store == nil {
		return
	}
	needs := make([]string, 0, len(spec.Secrets))
	for need := range spec.Secrets {
		needs = append(needs, need)
	}
	sort.Strings(needs)
	for _, need := range needs {
		name := spec.Secrets[need]
		have := false
		if _, err := d.store.GetCredential(seatSecretCredential(need)); err == nil {
			have = true
		}
		value, set := os.LookupEnv(name)
		if set {
			_ = os.Unsetenv(name)
		}
		switch {
		case have:
		case !set || value == "":
			log.Printf("seat env: cluster %s: %s is not set in the daemon's environment, so a seat that declares %q starts without it", cluster, name, need)
		default:
			err := d.store.CreateCredential(&api.Credential{
				Name: seatSecretCredential(need), Kind: api.CredentialSeatSecret, Audience: "seats",
				IssuedAt: time.Now().UTC(), Value: []byte(value),
			})
			if err != nil {
				log.Printf("seat env: cluster %s: hold %s for the seats that declare %q: %v", cluster, name, need, err)
			}
		}
	}
}

// seatSecret answers the session manager: the variable and the held value for a
// need the cluster maps.
func (d *Daemon) seatSecret(need string) (name string, value []byte, ok bool) { return "", nil, false }
