package daemon

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
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
	if d.driver == nil {
		return
	}
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
	cluster := "(none for this socket)"
	if cl != nil {
		cluster = cl.Name
	}
	d.driver.SetSeatEnv(spec.PathPrepend, spec.Unset)
	if len(spec.PathPrepend) > 0 || len(spec.Unset) > 0 {
		log.Printf("seat env: cluster %s puts %d directories in front of PATH and removes %d more variables in every seat", cluster, len(spec.PathPrepend), len(spec.Unset))
	}
	if vars := spec.SecretVars(); len(vars) > 0 {
		// Names only, in the log and on the tmux argument list. A cluster that
		// writes secrets: {} keeps every seat's environment as it was.
		d.driver.SetSeatSecretNames(vars)
		// Off the start path: a tmux that does not answer must not hold the daemon
		// back. The first seat spawn waits for it through spawnGate, with a bound.
		done := make(chan struct{})
		d.seatSecretMu.Lock()
		d.seatStripDone = done
		d.seatSecretMu.Unlock()
		d.seatStrip.Add(1)
		go func() {
			defer d.seatStrip.Done()
			defer close(done)
			if err := d.driver.StripServerEnv(); err != nil {
				log.Printf("seat env: %v", err)
			}
		}()
		log.Printf("seat env: cluster %s keeps %d seat secret variable(s) out of the tmux server and every pane: %s", cluster, len(vars), strings.Join(vars, ", "))
		d.captureSeatSecrets(cluster, spec)
		if d.sessMgr != nil {
			dir := filepath.Join(filepath.Dir(socketPath), "seat-secrets")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				log.Printf("seat env: no private directory for the seat secret hand-over, so seats start without their secrets: %v", err)
			} else {
				_ = os.Chmod(dir, 0o700)
				d.sessMgr.SeatSecrets = d
				d.sessMgr.SeatSecretDir = dir
				d.sessMgr.SeatSecretWait = 10 * time.Second
			}
			d.sessMgr.SpawnGate = d.spawnGate
		}
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
	d.seatSecretMu.Lock()
	d.seatSecretVars = make(map[string]string, len(spec.Secrets))
	for need, name := range spec.Secrets {
		d.seatSecretVars[need] = name
	}
	d.seatSecretMu.Unlock()
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
func (d *Daemon) seatSecret(need string) (name string, value []byte, ok bool) {
	d.seatSecretMu.Lock()
	name = d.seatSecretVars[need]
	d.seatSecretMu.Unlock()
	if name == "" || d.store == nil {
		return "", nil, false
	}
	v, err := d.store.RevealCredentialValue(seatSecretCredential(need))
	if err != nil || len(v) == 0 {
		return "", nil, false
	}
	return name, v, true
}

// SeatSecret lets the session manager ask the daemon for a need.
func (d *Daemon) SeatSecret(need string) (string, []byte, bool) { return d.seatSecret(need) }

// spawnGate holds a seat spawn until the strip of the tmux server environment has
// finished, for at most the bound, and returns a note when it had not. A seat
// that starts before the strip ends carries the note, and marvel doctor shows it.
func (d *Daemon) spawnGate() string {
	d.seatSecretMu.Lock()
	done := d.seatStripDone
	vars := make([]string, 0, len(d.seatSecretVars))
	for _, v := range d.seatSecretVars {
		vars = append(vars, v)
	}
	d.seatSecretMu.Unlock()
	if done == nil {
		return ""
	}
	bound := d.seatStripBound
	if bound <= 0 {
		bound = 10 * time.Second
	}
	// Once one spawn has waited the whole bound, later spawns do not each wait it
	// again; they carry the note until the strip finishes.
	d.seatSecretMu.Lock()
	late := d.seatStripLate
	d.seatSecretMu.Unlock()
	if late {
		bound = 0
	}
	timer := time.NewTimer(bound)
	defer timer.Stop()
	select {
	case <-done:
		return ""
	case <-timer.C:
		sort.Strings(vars)
		note := fmt.Sprintf("started before the tmux server environment was stripped of %s (not finished in %s); the pane still removes it itself", strings.Join(vars, ", "), d.boundOf())
		d.seatSecretMu.Lock()
		d.seatStripLate = true
		d.seatSecretMu.Unlock()
		log.Printf("seat env: %s", note)
		return note
	}
}

// boundOf is the strip wait a spawn is given the first time.
func (d *Daemon) boundOf() time.Duration {
	if d.seatStripBound > 0 {
		return d.seatStripBound
	}
	return 10 * time.Second
}
