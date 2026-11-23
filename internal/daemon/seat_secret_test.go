package daemon

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/tmux"
)

const (
	seatSecretVar  = "BEADS_DOLT_PASSWORD"
	seatSecretSock = "/tmp/m-seatsecretstore.sock"
	seatSecretYAML = "clusters:\n  - name: mine\n    socket: " + seatSecretSock + "\n    seat_env:\n      secrets:\n        bd: BEADS_DOLT_PASSWORD\n"
)

// seatSecretDaemon is a daemon with a store and a driver on a tmux that does
// nothing, reading the given cluster config. The log is captured so a test can
// prove no value reached it.
func seatSecretDaemon(t *testing.T, yaml string) (*Daemon, *bytes.Buffer) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".marvel"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".marvel", "config.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	drv, err := tmux.NewDriver()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &Daemon{driver: drv, store: api.NewStore()}, &buf
}

// At start the daemon moves a declared secret out of its own environment into
// the in-memory store, once, and the variable is gone from the process.
func TestLoadSeatEnvMovesADeclaredSecretIntoTheStore(t *testing.T) {
	t.Setenv(seatSecretVar, "mxfake-0042")
	d, logs := seatSecretDaemon(t, seatSecretYAML)
	d.loadSeatEnv(seatSecretSock)

	meta, err := d.store.GetCredential("seat-secret/bd")
	if err != nil {
		t.Fatalf("no seat-secret credential for the need: %v", err)
	}
	if meta.Kind != api.CredentialSeatSecret || meta.Persist {
		t.Errorf("credential = kind %q persist %v; want a transient seat-secret", meta.Kind, meta.Persist)
	}
	if v, err := d.store.RevealCredentialValue("seat-secret/bd"); err != nil || string(v) != "mxfake-0042" {
		t.Errorf("stored value does not match what the environment held: %v", err)
	}
	if _, still := os.LookupEnv(seatSecretVar); still {
		t.Errorf("%s is still in the daemon's environment", seatSecretVar)
	}
	if strings.Contains(logs.String(), "mxfake-0042") {
		t.Error("the value reached the log")
	}
}

// A cluster that declares nothing keeps its environment as it was, and the
// store stays empty.
func TestLoadSeatEnvWithNoSecretsTakesNothingFromTheEnvironment(t *testing.T) {
	t.Setenv(seatSecretVar, "mxfake-0042")
	d, _ := seatSecretDaemon(t, "clusters:\n  - name: mine\n    socket: "+seatSecretSock+"\n")
	d.loadSeatEnv(seatSecretSock)
	if got := os.Getenv(seatSecretVar); got != "mxfake-0042" {
		t.Errorf("the variable was touched with no mapping: %q", got)
	}
	if n := len(d.store.ListCredentials()); n != 0 {
		t.Errorf("%d credentials with no mapping", n)
	}
}

// A value the store already holds is never overwritten by the environment.
func TestLoadSeatEnvKeepsAValueTheStoreAlreadyHolds(t *testing.T) {
	t.Setenv(seatSecretVar, "mxfake-second")
	d, logs := seatSecretDaemon(t, seatSecretYAML)
	if err := d.store.CreateCredential(&api.Credential{Name: "seat-secret/bd", Kind: api.CredentialSeatSecret, Value: []byte("mxfake-first")}); err != nil {
		t.Fatal(err)
	}
	d.loadSeatEnv(seatSecretSock)
	if v, _ := d.store.RevealCredentialValue("seat-secret/bd"); string(v) != "mxfake-first" {
		t.Errorf("the stored value was replaced: %q", v)
	}
	if strings.Contains(logs.String(), "mxfake-") {
		t.Error("a value reached the log")
	}
}

// A declared variable the daemon does not hold is logged by name, makes no
// credential, and is not an error.
func TestLoadSeatEnvWithAMissingVariableNamesItAndTakesNothing(t *testing.T) {
	os.Unsetenv(seatSecretVar) //nolint:errcheck
	d, logs := seatSecretDaemon(t, seatSecretYAML)
	d.loadSeatEnv(seatSecretSock)
	if n := len(d.store.ListCredentials()); n != 0 {
		t.Errorf("%d credentials for a variable the daemon does not hold", n)
	}
	if !strings.Contains(logs.String(), seatSecretVar) || !strings.Contains(logs.String(), "not set") {
		t.Errorf("the log does not say %s is not set: %s", seatSecretVar, logs.String())
	}
}

// credential.reveal never answers for a seat secret, even where reveal is on.
func TestCredentialRevealRefusesASeatSecret(t *testing.T) {
	d := newHandlerDaemon(t)
	if err := d.store.CreateCredential(&api.Credential{Name: "seat-secret/bd", Kind: api.CredentialSeatSecret, Value: []byte("mxfake-0042")}); err != nil {
		t.Fatal(err)
	}
	d.revealAllowed.Store(true)
	resp := d.dispatchAs(daemonrpc.Request{Method: "credential.reveal", Params: nameParams(t, "seat-secret/bd")}, localCaller())
	if resp.Error == "" {
		t.Fatal("reveal answered for a seat secret")
	}
	b, _ := json.Marshal(resp)
	if strings.Contains(string(b), "mxfake-0042") {
		t.Error("the refusal carries the value")
	}
}

// The daemon answers the session manager for a mapped need from the store, and
// for nothing else.
func TestTheDaemonAnswersASeatSecretNeedFromTheStore(t *testing.T) {
	t.Setenv(seatSecretVar, "mxfake-0042")
	d, _ := seatSecretDaemon(t, seatSecretYAML)
	d.loadSeatEnv(seatSecretSock)
	name, value, ok := d.seatSecret("bd")
	if !ok || name != seatSecretVar || string(value) != "mxfake-0042" {
		t.Errorf("seatSecret(bd) = %q, ok %v; want the mapped variable and the held value", name, ok)
	}
	if _, _, ok := d.seatSecret("other"); ok {
		t.Error("an unmapped need was answered")
	}
}
