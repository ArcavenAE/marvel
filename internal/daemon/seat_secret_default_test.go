package daemon

import (
	"os"
	"strings"
	"testing"
)

const noSecretsYAML = "clusters:\n  - name: mine\n    socket: " + seatSecretSock + "\n"

// A cluster that says nothing about secrets still keeps the dolt password out
// of the process environment and holds it for the seats that declare bd.
func TestADaemonWithNoSecretsBlockHoldsTheDoltPasswordByDefault(t *testing.T) {
	t.Setenv(seatSecretVar, "mxfake-0042")
	d, logs := seatSecretDaemon(t, noSecretsYAML)
	d.loadSeatEnv(seatSecretSock)

	if _, err := d.store.GetCredential("seat-secret/bd"); err != nil {
		t.Fatalf("the default did not hold the bd need: %v", err)
	}
	if v, ok := os.LookupEnv(seatSecretVar); ok {
		t.Errorf("the variable is still in the daemon's environment (%d bytes)", len(v))
	}
	if strings.Contains(logs.String(), "mxfake-0042") {
		t.Error("the log holds the value")
	}
}

// `secrets: {}` opts out: nothing is held and the environment is left alone.
func TestAnEmptySecretsBlockLeavesTheDaemonEnvironmentAlone(t *testing.T) {
	t.Setenv(seatSecretVar, "mxfake-0042")
	d, _ := seatSecretDaemon(t, noSecretsYAML+"    seat_env:\n      secrets: {}\n")
	d.loadSeatEnv(seatSecretSock)

	if _, err := d.store.GetCredential("seat-secret/bd"); err == nil {
		t.Error("an opted-out cluster holds a bd secret")
	}
	if os.Getenv(seatSecretVar) == "" {
		t.Error("an opted-out cluster lost the variable")
	}
}
