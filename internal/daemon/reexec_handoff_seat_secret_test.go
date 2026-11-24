package daemon

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// A seat secret crosses a reexec in memory only, through the same anonymous pipe
// the leaf seed uses: the read end stays open across the exec, only its number
// travels, and the successor reads it once. The value is a fake marker.

func putSeatSecret(t *testing.T, d *Daemon, value string) {
	t.Helper()
	c := api.Credential{Name: "seat-secret/bd", Kind: api.CredentialSeatSecret, Audience: "seats", Value: []byte(value)}
	if err := d.store.CreateCredential(&c); err != nil {
		t.Fatal(err)
	}
}

func TestASeatSecretSurvivesAReexec(t *testing.T) {
	d := newHandlerDaemon(t)
	putSeatSecret(t, d, handoffCanary)
	d.handoffProbe = capableTarget
	var r reexecSeam
	d.seamReexec(&r)
	logs := captureLog(t)

	if err := d.ReexecTo("/bin/marvel-target"); err != nil {
		t.Fatal(err)
	}
	var fdKV string
	for _, kv := range r.envv {
		if strings.HasPrefix(kv, handoffEnvVar+"=") {
			fdKV = kv
		}
	}
	if fdKV == "" {
		t.Fatalf("no %s in the exec environment: the seat secret was not carried\n%s", handoffEnvVar, logs.String())
	}
	fd, _ := strconv.Atoi(strings.TrimPrefix(fdKV, handoffEnvVar+"="))
	t.Setenv(handoffEnvVar, strconv.Itoa(fd))
	creds, got := receiveReexecHandoff()
	if got.State != handoffRead {
		t.Fatalf("read state = %v (%v), want ok", got.State, got.Err)
	}
	found := false
	for _, c := range creds {
		if c.Name == "seat-secret/bd" && c.Kind == api.CredentialSeatSecret && string(c.Value) == handoffCanary {
			found = true
		}
	}
	if !found {
		t.Errorf("the successor did not receive the seat secret: %d credentials", len(creds))
	}
	if !isClosed(fd) {
		t.Error("the handoff descriptor is still open after the read")
	}
}

// filesHolding lists the regular files under root that contain value.
func filesHolding(root, value string) []string {
	var found []string
	_ = filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil || !e.Type().IsRegular() {
			return nil
		}
		if b, err := os.ReadFile(p); err == nil && strings.Contains(string(b), value) {
			found = append(found, p)
		}
		return nil
	})
	return found
}

// The value is in no file under the daemon's own home or temp directory, in no
// variable of the exec environment, and not in the log, after a carried reexec.
// Both directories are scratch the test made before the daemon started, so the
// scan never reads the real marvel home. A control file with the value, put
// where the scan looks, must be found, so an empty scan cannot pass for nothing.
func TestACarriedSeatSecretLeavesNoFileNoVariableNoLogLine(t *testing.T) {
	home, tmp := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TMPDIR", tmp)
	d := newHandlerDaemon(t)
	putSeatSecret(t, d, handoffCanary)
	d.handoffProbe = capableTarget
	var r reexecSeam
	d.seamReexec(&r)
	logs := captureLog(t)
	if err := d.ReexecTo("/bin/marvel-target"); err != nil {
		t.Fatal(err)
	}
	carried := false
	for _, kv := range r.envv {
		if strings.HasPrefix(kv, handoffEnvVar+"=") {
			carried = true
		}
		if strings.Contains(kv, handoffCanary) {
			t.Errorf("the exec environment carries the value: %q", strings.SplitN(kv, "=", 2)[0])
		}
	}
	if !carried {
		t.Fatal("nothing was carried, so the scans below prove nothing")
	}
	if strings.Contains(logs.String(), handoffCanary) {
		t.Error("the log carries the value")
	}
	control := filepath.Join(tmp, "control")
	if err := os.WriteFile(control, []byte("x"+handoffCanary+"x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := filesHolding(tmp, handoffCanary); len(got) != 1 || got[0] != control {
		t.Fatalf("the scan did not find the control file it was given: %v", got)
	}
	if err := os.Remove(control); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{home, tmp} {
		if got := filesHolding(root, handoffCanary); len(got) != 0 {
			t.Errorf("a file under the scratch %s holds the value: %v", filepath.Base(root), got)
		}
	}
}

// The predecessor's own environment does not hold the value at the exec: the
// daemon moved it into the store at start, so the successor cannot get it from
// the environment, only from the pipe.
func TestTheExecEnvironmentOfADaemonThatDeclaresASecretHoldsNoValue(t *testing.T) {
	t.Setenv(seatSecretVar, handoffCanary)
	d, _ := seatSecretDaemon(t, seatSecretYAML)
	d.loadSeatEnv(seatSecretSock)
	d.handoffProbe = capableTarget
	var r reexecSeam
	d.seamReexec(&r)
	if err := d.ReexecTo("/bin/marvel-target"); err != nil {
		t.Fatal(err)
	}
	for _, kv := range r.envv {
		if strings.HasPrefix(kv, seatSecretVar+"=") || strings.Contains(kv, handoffCanary) {
			t.Errorf("the exec environment holds the secret variable: %q", strings.SplitN(kv, "=", 2)[0])
		}
	}
}
